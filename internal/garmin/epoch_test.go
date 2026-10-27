package garmin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
)

func TestEpochBindingSurvivesRefreshAndCancelsOnDrop(t *testing.T) {
	t.Parallel()

	l := NewLazy(func(context.Context) (device.Device, error) { return &leaseDevice{}, nil })

	t.Cleanup(func() { closeOwner(t, l) })
	lease := acquireLease(t, l)
	epoch := lease.Epoch()

	bound, release, err := l.BindEpoch(t.Context(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	lease.Service().Refresh()

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if bound.Err() != nil {
		t.Fatal("cache Refresh invalidated connection binding")
	}

	if err := l.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	select {
	case <-bound.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Drop did not cancel network/job scope")
	}

	if cause := context.Cause(bound); !errors.Is(cause, ErrSessionChanged) || !errors.Is(cause, device.ErrDisconnected) {
		t.Fatalf("epoch cause: %v", cause)
	}

	release()
	release()

	if _, _, err := l.BindEpoch(t.Context(), epoch); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("old binding accepted: %v", err)
	}
}

func TestEpochBindingReleaseJoinsWithoutCancellingCaller(t *testing.T) {
	t.Parallel()

	l := NewLazy(func(context.Context) (device.Device, error) { return &leaseDevice{}, nil })

	t.Cleanup(func() { closeOwner(t, l) })
	lease := acquireLease(t, l)

	bound, release, err := l.BindEpoch(t.Context(), lease.Epoch())
	if err != nil {
		t.Fatal(err)
	}

	release()

	if !errors.Is(bound.Err(), context.Canceled) || t.Context().Err() != nil {
		t.Fatal("binding release cancelled parent or left worker scope alive")
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}
