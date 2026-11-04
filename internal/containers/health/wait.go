package health

import "context"

// acquire takes a slot in sem, or reports false when ctx ends first.
func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}
