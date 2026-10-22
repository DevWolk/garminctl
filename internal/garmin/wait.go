package garmin

import (
	"context"
	"time"
)

// pullRetryDelay is how long to wait for Connect to queue a file before
// pulling again.
const pullRetryDelay = 1500 * time.Millisecond

// acquire takes a slot in sem, or reports false when ctx ends first.
func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// sleepCtx waits for d unless ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
