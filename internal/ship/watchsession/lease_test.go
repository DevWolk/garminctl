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

func TestCancelledAcquisitionClosesSessionRetiredMeanwhile(t *testing.T) {
	t.Parallel()

	dev := &deadlineDevice{}
	manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

	t.Cleanup(func() { closeManager(t, manager) })

	lease, err := manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	// A request holds the slot but has not become the active group yet when
	// the connection is dropped, so DropAt leaves the retired transport to it.
	manager.slot <- struct{}{}

	if err := manager.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := manager.acquireReserved(ctx, 0, func() {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquireReserved error = %v, want context.Canceled", err)
	}

	if closes := dev.closes.Load(); closes != 1 {
		t.Fatalf("retired transport closed %d times after the cancelled request left, want 1", closes)
	}

	if manager.Current() != nil {
		t.Fatal("a retired session is still current")
	}
}
