package connect

import (
	"context"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func acquireLease(t *testing.T, manager *watchsession.Manager) *watchsession.Lease {
	t.Helper()

	lease, err := manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := lease.Release(); err != nil {
			t.Error(err)
		}
	})

	return lease
}

func closeOwner(t *testing.T, manager *watchsession.Manager) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Close(ctx); err != nil {
		t.Error(err)
	}
}
