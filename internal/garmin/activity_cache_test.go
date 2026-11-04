package garmin

import (
	"testing"
	"time"
)

func waitActivityBarrier(t *testing.T, entered <-chan struct{}) {
	t.Helper()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("activity barrier was not reached")
	}
}
