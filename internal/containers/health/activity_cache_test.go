package health

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type activityBarrierDevice struct {
	device.Device
	mu           sync.Mutex
	reads        map[string]int
	blocked      string
	entered      chan struct{}
	gate         <-chan struct{}
	trace        chan string
	ignoreCancel bool
	closeOnce    sync.Once
	closeErr     error
}

func (d *activityBarrierDevice) List(ctx context.Context, path string) ([]device.Entry, error) {
	entries, err := d.Device.List(ctx, path)
	if path == "GARMIN/Activity" {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	}

	return entries, err
}

func (d *activityBarrierDevice) Close() error {
	d.closeOnce.Do(func() { d.closeErr = d.Device.Close() })
	return d.closeErr
}

func (d *activityBarrierDevice) Read(ctx context.Context, path string) ([]byte, error) {
	if strings.HasPrefix(path, "GARMIN/Activity/") {
		if d.trace != nil {
			d.trace <- path
		}

		d.mu.Lock()
		d.reads[path]++
		first := d.reads[path] == 1 && path == d.blocked
		d.mu.Unlock()

		if first {
			close(d.entered)

			if d.ignoreCancel {
				<-d.gate
				return d.Device.Read(ctx, path)
			}

			select {
			case <-d.gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	return d.Device.Read(ctx, path)
}

func activityTestDevice(t *testing.T, count int) (*activityBarrierDevice, []device.Entry) {
	t.Helper()
	root := testdump.Root(t)

	dev, err := device.NewDirFS(root)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile("../../fit/testdata/WalkingWalk.fit")
	if err != nil {
		t.Fatal(err)
	}

	entries := make([]device.Entry, count)
	for i := range count {
		name := fmt.Sprintf("activity-%02d.fit", i)
		if err := dev.Write(t.Context(), "GARMIN/Activity/"+name, data); err != nil {
			t.Fatal(err)
		}

		entries[i] = device.Entry{Name: name, Size: int64(len(data))}
	}

	tracked := &activityBarrierDevice{Device: dev, reads: make(map[string]int), entered: make(chan struct{})}

	t.Cleanup(func() {
		if err := tracked.Close(); err != nil {
			t.Error(err)
		}
	})

	return tracked, entries
}

func waitActivityBarrier(t *testing.T, entered <-chan struct{}) {
	t.Helper()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("activity barrier was not reached")
	}
}

func awaitActivityResult(t *testing.T, done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("activity job did not finish")
	}
}

func TestActivityCacheMergeRetainsConcurrentIndependentEntries(t *testing.T) {
	t.Parallel()
	dev, entries := activityTestDevice(t, 2)

	gate := make(chan struct{})
	defer close(gate)

	dev.gate, dev.blocked = gate, "GARMIN/Activity/"+entries[0].Name
	svc := newActivityReader(fit.MustProfile(), watchsession.New(dev))
	cp := filepath.Join(t.TempDir(), "activities.json")

	done := make(chan error, 1)
	go func() { done <- svc.decodeActivities(t.Context(), cp, entries[:1]) }()

	waitActivityBarrier(t, dev.entered)

	if err := newActivityReader(fit.MustProfile(), watchsession.New(dev)).decodeActivities(t.Context(), cp, entries[1:]); err != nil {
		t.Fatal(err)
	}

	gate <- struct{}{}

	awaitActivityResult(t, done)
	activityCacheMu.Lock()
	cache := loadActivityCache(cp)
	activityCacheMu.Unlock()

	if len(cache.Entries) != 2 {
		t.Fatalf("independent cache entry lost: %v", cache.Entries)
	}
}

// cacheWriteCounter reports how often the cache file was replaced between the
// activity reads of one decode run.
type cacheWriteCounter struct {
	device.Device
	cachePath string
	last      os.FileInfo
	writes    int
}

func (d *cacheWriteCounter) Read(ctx context.Context, path string) ([]byte, error) {
	if strings.HasPrefix(path, "GARMIN/Activity/") {
		if info, err := os.Stat(d.cachePath); err == nil {
			if d.last == nil || !os.SameFile(d.last, info) {
				d.writes++
			}

			d.last = info
		}
	}

	return d.Device.Read(ctx, path)
}

func TestActivityDecodeDoesNotRewriteCacheForEveryFile(t *testing.T) {
	t.Parallel()
	dev, entries := activityTestDevice(t, 24)

	cp := filepath.Join(t.TempDir(), "activities.json")
	counter := &cacheWriteCounter{Device: dev, cachePath: cp}
	started := time.Now()

	if err := newActivityReader(fit.MustProfile(), watchsession.New(counter)).decodeActivities(t.Context(), cp, entries); err != nil {
		t.Fatal(err)
	}

	if allowed := 1 + int(time.Since(started)/activityFlushInterval); counter.writes > allowed {
		t.Fatalf("cache replaced %d times while decoding %d files, want at most %d", counter.writes, len(entries), allowed)
	}

	activityCacheMu.Lock()
	cache := loadActivityCache(cp)
	activityCacheMu.Unlock()

	if len(cache.Entries) != len(entries) {
		t.Fatalf("cache holds %d of %d decoded activities", len(cache.Entries), len(entries))
	}
}

func TestActivityDecodeKeepsFinishedFilesWhenInterrupted(t *testing.T) {
	t.Parallel()
	dev, entries := activityTestDevice(t, 3)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	gate := make(chan struct{})
	defer close(gate)

	dev.gate, dev.blocked = gate, "GARMIN/Activity/"+entries[2].Name
	cp := filepath.Join(t.TempDir(), "activities.json")
	done := make(chan error, 1)

	go func() {
		done <- newActivityReader(fit.MustProfile(), watchsession.New(dev)).decodeActivities(ctx, cp, entries)
	}()

	waitActivityBarrier(t, dev.entered)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted decode: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted decode did not return")
	}

	activityCacheMu.Lock()
	cache := loadActivityCache(cp)
	activityCacheMu.Unlock()

	if len(cache.Entries) != 2 {
		t.Fatalf("finished activities lost on interruption: %v", cache.Entries)
	}
}

func TestActivitiesWaitJoinsBackgroundAndReadsEachFileOnce(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	dev, entries := activityTestDevice(t, 16)

	gate := make(chan struct{})
	defer close(gate)

	dev.gate, dev.blocked = gate, "GARMIN/Activity/"+entries[0].Name
	svc := newActivityReader(fit.MustProfile(), watchsession.New(dev))

	first, err := svc.Activities(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}

	if first.Pending != len(entries) {
		t.Fatalf("pending=%d", first.Pending)
	}

	waitActivityBarrier(t, dev.entered)

	out := make(chan *ActivityList, 1)
	done := make(chan error, 1)

	go func() {
		list, err := newActivityReader(fit.MustProfile(), watchsession.New(dev)).Activities(t.Context(), true)
		out <- list

		done <- err
	}()

	gate <- struct{}{}

	awaitActivityResult(t, done)

	list := <-out
	if list.Pending != 0 || len(list.Activities) != len(entries) {
		t.Fatalf("incomplete result: %+v", list)
	}

	dev.mu.Lock()
	defer dev.mu.Unlock()

	for _, entry := range entries {
		if count := dev.reads["GARMIN/Activity/"+entry.Name]; count != 1 {
			t.Errorf("%s read %d times", entry.Name, count)
		}
	}
}

func TestActivityJoinCancellationDoesNotCancelExistingJob(t *testing.T) {
	t.Parallel()
	cp := filepath.Join(t.TempDir(), "activities.json")

	job, started := reserveActivityJob(cp)
	if !started {
		t.Fatal("could not reserve job")
	}

	defer func() {
		activityJobs.Lock()
		delete(activityJobs.running, cp)
		close(job.done)
		activityJobs.Unlock()
	}()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := new(activityReader).decodeOrJoin(ctx, cp, nil, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}

	select {
	case <-job.done:
		t.Fatal("waiter cancelled shared job")
	default:
	}
}

func TestActivityCacheReplacementFailureCleansTemporaryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cp := filepath.Join(dir, "activities.json")
	if err := os.Mkdir(cp, 0o700); err != nil {
		t.Fatal(err)
	}
	// A nonempty directory forces replacement to fail on Unix and Windows.
	if err := os.WriteFile(filepath.Join(cp, "keep"), []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeActivityCache(cp, []ActivitySummary{{File: "new.fit"}}); err == nil {
		t.Fatal("failed cache replacement reported success")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 || entries[0].Name() != "activities.json" {
		t.Fatalf("temporary file leaked: %v", entries)
	}

	previous, err := os.ReadFile(filepath.Join(cp, "keep")) //nolint:gosec // fixed test filename under t.TempDir
	if err != nil || string(previous) != "previous" {
		t.Fatalf("previous cache changed: %q, %v", previous, err)
	}
}
