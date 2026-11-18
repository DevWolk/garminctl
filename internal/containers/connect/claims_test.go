package connect

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// pullRace orders two pulls of one queued message: the first lists, claims and
// starts downloading; only then may the second list the same message, and the
// first writes only after the second has skipped it or started its own download.
type pullRace struct {
	listings, downloads, acks atomic.Int32

	firstListed, firstDownloading, secondListed, secondDownloading, secondDone chan struct{}
}

func newPullRace() *pullRace {
	return &pullRace{
		firstListed: make(chan struct{}), firstDownloading: make(chan struct{}), secondListed: make(chan struct{}),
		secondDownloading: make(chan struct{}), secondDone: make(chan struct{}),
	}
}

func (r *pullRace) roundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case deviceMessagesPath:
		switch r.listings.Add(1) {
		case 1:
			close(r.firstListed)
		case 2:
			<-r.firstDownloading
			close(r.secondListed)
		}

		return syncResponse(req, syncMessages()), nil
	case syntheticFilePath:
		if r.downloads.Add(1) == 1 {
			close(r.firstDownloading)
			<-r.secondListed

			select {
			case <-r.secondDone:
			case <-r.secondDownloading:
			}
		} else {
			close(r.secondDownloading)
		}

		return syncResponse(req, []byte("synthetic payload")), nil
	case syntheticAckPath:
		r.acks.Add(1)
		return syncResponse(req, nil), nil
	default:
		return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
	}
}

func TestConcurrentPullsDeliverQueuedMessageOnce(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	race := newPullRace()
	config := Config{CacheDir: t.TempDir(), Claims: new(MessageClaims)}
	sess := watchsession.New(dev)
	client := syncTestClient(t, race.roundTrip)
	clouds := []*Cloud{NewCloud(sess, client, config), NewCloud(sess, client, config)}
	results := make([][]PulledFile, len(clouds))
	errs := make([]error, len(clouds))

	var wg sync.WaitGroup

	for i, cloud := range clouds {
		if i == 1 {
			<-race.firstListed
		}

		wg.Go(func() {
			results[i], errs[i] = cloud.Pull(context.WithoutCancel(t.Context()), nil)

			if i == 1 {
				close(race.secondDone)
			}
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("pull %d: %v", i, err)
		}
	}

	if delivered := len(results[0]) + len(results[1]); delivered != 1 || race.downloads.Load() != 1 || race.acks.Load() != 1 {
		t.Fatalf("message delivered %d times (downloads=%d acks=%d): %+v", delivered, race.downloads.Load(), race.acks.Load(), results)
	}
}

func TestPullWithStaleListingSkipsDeliveredMessage(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	var downloads, acks atomic.Int32

	// Connect still lists the message as new after the first acknowledgement,
	// as a listing taken before that acknowledgement would.
	client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case deviceMessagesPath:
			return syncResponse(req, syncMessages()), nil
		case syntheticFilePath:
			downloads.Add(1)
			return syncResponse(req, []byte("synthetic payload")), nil
		case syntheticAckPath:
			acks.Add(1)
			return syncResponse(req, nil), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	})

	config := Config{CacheDir: t.TempDir(), Claims: new(MessageClaims)}
	sess := watchsession.New(dev)

	for i := range 2 {
		if _, err := NewCloud(sess, client, config).Pull(t.Context(), nil); err != nil {
			t.Fatalf("pull %d: %v", i, err)
		}
	}

	if downloads.Load() != 1 || acks.Load() != 1 {
		t.Fatalf("delivered message fetched again: downloads=%d acks=%d", downloads.Load(), acks.Load())
	}
}

// firstAckFails serves one queued message whose first acknowledgement fails,
// so Connect keeps listing the message as new.
func firstAckFails(downloads, acks *atomic.Int32) syncTransport {
	return func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case deviceMessagesPath:
			return syncResponse(req, syncMessages()), nil
		case syntheticFilePath:
			downloads.Add(1)
			return syncResponse(req, []byte("synthetic payload")), nil
		case syntheticAckPath:
			if acks.Add(1) == 1 {
				response := syncResponse(req, []byte(`{"error":"synthetic"}`))
				response.StatusCode = http.StatusBadRequest

				return response, nil
			}

			return syncResponse(req, nil), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	}
}

func TestPullRetriesFailedAckWithoutWritingAgain(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)

	dev, err := device.NewDirFS(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	var downloads, acks atomic.Int32

	client := syncTestClient(t, firstAckFails(&downloads, &acks))

	config := Config{CacheDir: t.TempDir(), Claims: new(MessageClaims)}
	sess := watchsession.New(dev)

	for i := range 3 {
		if _, err := NewCloud(sess, client, config).Pull(t.Context(), nil); err != nil {
			t.Fatalf("pull %d: %v", i, err)
		}
	}

	written, err := os.ReadDir(filepath.Join(root, "GARMIN", "NewFiles"))
	if err != nil {
		t.Fatal(err)
	}

	if downloads.Load() != 1 || acks.Load() != 2 || len(written) != 1 {
		t.Fatalf("downloads=%d acks=%d files=%d, want 1, 2 and 1", downloads.Load(), acks.Load(), len(written))
	}
}

func TestQueuedPullDoesNotWaitForMessageAnotherPullDelivers(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	var listings atomic.Int32

	client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != deviceMessagesPath {
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}

		listings.Add(1)

		return syncResponse(req, syncMessages()), nil
	})

	claims := new(MessageClaims)
	if got, _ := claims.claim(42); got != claimDeliver {
		t.Fatalf("first claim = %v", got)
	}

	cloud := NewCloud(watchsession.New(dev), client, Config{CacheDir: t.TempDir(), Claims: claims})

	pulled, delivering, err := cloud.pullQueued(t.Context(), "settings-change")
	if err != nil || len(pulled) != 0 || !delivering {
		t.Fatalf("pulled=%+v delivering=%t err=%v", pulled, delivering, err)
	}

	if listings.Load() != 1 {
		t.Fatalf("queue listed %d times while another pull held the message", listings.Load())
	}
}

func TestPullReportsEarlierWriteOnceConnectIsTold(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	var downloads, acks atomic.Int32

	client := syncTestClient(t, firstAckFails(&downloads, &acks))

	config := Config{CacheDir: t.TempDir(), Claims: new(MessageClaims)}
	sess := watchsession.New(dev)

	first, err := NewCloud(sess, client, config).Pull(t.Context(), nil)
	if err != nil || len(first) != 1 || first[0].Path == "" || first[0].Error == "" {
		t.Fatalf("first pull: %+v %v", first, err)
	}

	second, err := NewCloud(sess, client, config).Pull(t.Context(), nil)
	if err != nil || len(second) != 1 {
		t.Fatalf("second pull: %+v %v", second, err)
	}

	if second[0].Path != first[0].Path || second[0].Bytes != first[0].Bytes || second[0].Error != "" {
		t.Fatalf("acknowledged file reported as %+v, written as %+v", second[0], first[0])
	}

	third, err := NewCloud(sess, client, config).Pull(t.Context(), nil)
	if err != nil || len(third) != 0 || downloads.Load() != 1 {
		t.Fatalf("delivered and acknowledged message handled again: %+v %v downloads=%d", third, err, downloads.Load())
	}
}

func TestSettingsChangeNamesRunningSyncInsteadOfEmptyQueue(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/device-service/deviceservice/device-info/settings/"):
			return syncResponse(req, nil), nil
		case req.URL.Path == deviceMessagesPath:
			return syncResponse(req, syncMessages()), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	})

	claims := new(MessageClaims)
	claims.claim(42)

	cloud := NewCloud(watchsession.New(dev), client, Config{CacheDir: t.TempDir(), Claims: claims})

	res, err := cloud.SetSettings(t.Context(), map[string]any{"timeFormat": "time_twenty_four_hr"})
	if err != nil || len(res.Pulled) != 0 || !strings.Contains(res.Note, "running sync") {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}
