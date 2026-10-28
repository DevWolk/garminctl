package garmin

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
)

type cacheTestDevice struct {
	device.Device
	mu         sync.Mutex
	data       []byte
	lastRead   []byte
	reads      int
	reconnects int
	entered    chan struct{}
	release    <-chan struct{}
	onCount    func()
}

type cacheReadResult struct {
	data []byte
	err  error
}

func (d *cacheTestDevice) Reconnects() int {
	if d.onCount != nil {
		d.onCount()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.reconnects
}

func (d *cacheTestDevice) Read(ctx context.Context, _ string) ([]byte, error) {
	d.mu.Lock()
	d.reads++
	data := bytes.Clone(d.data)
	d.lastRead = data
	blocked := d.reads == 1 && d.release != nil
	d.mu.Unlock()

	if blocked {
		close(d.entered)

		select {
		case <-d.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return data, nil
}

func (d *cacheTestDevice) replace(data string, reconnect bool) {
	d.mu.Lock()

	d.data = []byte(data)
	if reconnect {
		d.reconnects++
	}
	d.mu.Unlock()
}

func (d *cacheTestDevice) readCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.reads
}

func readCachedTestValue(t *testing.T, svc *Service, cached bool) []byte {
	t.Helper()

	data, err := svc.read(t.Context(), "GARMIN/example.fit", cached)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestFileCacheBytesDoNotEscape(t *testing.T) {
	t.Parallel()

	d := &cacheTestDevice{data: []byte("original")}
	svc := New(d)
	first := readCachedTestValue(t, svc, true)
	first[0] = 'x'

	d.mu.Lock()
	d.lastRead[1] = 'y' // a transport's buffer must not be the cache's storage
	d.mu.Unlock()

	second := readCachedTestValue(t, svc, true)
	if string(second) != "original" {
		t.Fatalf("caller/transport modified the cache: %q", second)
	}

	second[2] = 'z'

	if got := readCachedTestValue(t, svc, true); string(got) != "original" || d.readCount() != 1 {
		t.Fatalf("cache-hit buffer escaped: %q, reads=%d", got, d.readCount())
	}
}

func TestFreshReadStillPopulatesFileCache(t *testing.T) {
	t.Parallel()

	d := &cacheTestDevice{data: []byte("first")}
	svc := New(d)
	readCachedTestValue(t, svc, true)
	d.replace("second", false)

	if got := readCachedTestValue(t, svc, true); string(got) != "first" {
		t.Fatalf("cached lookup changed policy: %q", got)
	}

	if got := readCachedTestValue(t, svc, false); string(got) != "second" {
		t.Fatalf("fresh read did not bypass lookup: %q", got)
	}

	if got := readCachedTestValue(t, svc, true); string(got) != "second" || d.readCount() != 2 {
		t.Fatalf("fresh read failed to populate cache: %q, reads=%d", got, d.readCount())
	}
}

func TestLateReadCannotReplaceNewCacheGeneration(t *testing.T) {
	t.Parallel()

	for _, boundary := range []string{"refresh", "invalidate", "reconnect"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()

			release := make(chan struct{})

			var releaseOnce sync.Once

			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)

			d := &cacheTestDevice{data: []byte("old"), entered: make(chan struct{}), release: release}
			svc := New(d)
			result := make(chan cacheReadResult, 1)
			ctx := t.Context()

			go func() {
				data, err := svc.read(ctx, "GARMIN/example.fit", false)
				result <- cacheReadResult{data, err}
			}()

			<-d.entered

			d.replace("new", boundary == "reconnect")

			switch boundary {
			case "refresh":
				svc.Refresh()
			case "invalidate":
				svc.invalidate("GARMIN/example.fit")
			}

			if got := readCachedTestValue(t, svc, true); string(got) != "new" {
				t.Fatalf("new generation read = %q", got)
			}

			unblock()

			if got := <-result; got.err != nil || string(got.data) != "old" {
				t.Fatalf("barrier did not preserve the old read snapshot: %q, %v", got.data, got.err)
			}

			if got := readCachedTestValue(t, svc, true); string(got) != "new" || d.readCount() != 2 {
				t.Fatalf("late read polluted new generation: %q, reads=%d", got, d.readCount())
			}
		})
	}
}
