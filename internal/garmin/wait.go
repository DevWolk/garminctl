package garmin

import (
	"context"
	"time"
)

// pullRetryDelay is how long to wait for Connect to queue a file before
// pulling again.
const pullRetryDelay = 1500 * time.Millisecond

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
