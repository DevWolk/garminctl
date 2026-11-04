package connect

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
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
	case "/device-service/devicemessage/message/42":
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
