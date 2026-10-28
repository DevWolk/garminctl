package watchsession

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
)

type deadlineDevice struct {
	device.Device
	closes atomic.Int32
}

func (d *deadlineDevice) Close() error { d.closes.Add(1); return nil }

func acquireManagerLease(t *testing.T, manager *Manager) *Lease {
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

func closeManager(t *testing.T, manager *Manager) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := manager.Close(ctx); err != nil {
		t.Error(err)
	}
}

func TestLeaseGroupDeadlineDoesNotCancelParentOrDropUnusedSession(t *testing.T) {
	t.Parallel()

	dev := &deadlineDevice{}
	l := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

	warm := acquireManagerLease(t, l)
	if err := warm.Release(); err != nil {
		t.Fatal(err)
	}

	l.operationLimit = 50 * time.Millisecond

	t.Cleanup(func() { closeManager(t, l) })

	parent, cancel := context.WithCancel(t.Context())
	defer cancel()

	lease, err := l.Acquire(parent)
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()
	select {
	case <-lease.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("group deadline did not expire")
	}

	if !errors.Is(lease.Context().Err(), context.DeadlineExceeded) || parent.Err() != nil {
		t.Fatal("group limit failed or cancelled parent")
	}

	if dev.closes.Load() != 0 || l.Current() != lease.Session() {
		t.Fatal("pre-I/O deadline dropped unused healthy session")
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	next := acquireManagerLease(t, l)
	if next.Epoch() != epoch {
		t.Fatal("unused group deadline replaced connection")
	}
}
