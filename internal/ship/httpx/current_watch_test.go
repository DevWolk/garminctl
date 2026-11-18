package httpx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestCurrentWatchOfflineAdmission(t *testing.T) {
	t.Parallel()

	mgr := watchsession.NewManager(func(context.Context) (device.Device, error) {
		t.Fatal("optional watch helper attempted to open a transport")
		return nil, nil
	})

	t.Cleanup(func() { closeCurrentManager(t, mgr) })

	want := errors.New("offline body failed")

	out, err := WithCurrentWatch(t.Context(), mgr, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		if lease != nil {
			t.Fatal("unopened manager supplied a lease")
		}

		if _, err := mgr.Gate().TryMaintenance(ctx); !errors.Is(err, lifecycle.ErrBusy) {
			t.Fatalf("offline body was not admitted: %v", err)
		}

		return 42, want
	})
	if out != 42 || !errors.Is(err, want) || mgr.Gate().Snapshot().Operations != 0 {
		t.Fatalf("result=%v err=%v gate=%+v", out, err, mgr.Gate().Snapshot())
	}

	resume, err := mgr.Gate().TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resume()

	_, err = WithCurrentWatch(t.Context(), mgr, func(context.Context, *watchsession.Lease) (any, error) {
		t.Fatal("maintenance admitted optional work")
		return nil, nil
	})
	if !errors.Is(err, lifecycle.ErrMaintenance) {
		t.Fatalf("maintenance error=%v", err)
	}
}

type currentWatchDevice struct {
	device.Device
	closes *atomic.Int32
}

func (d currentWatchDevice) Close() error {
	d.closes.Add(1)
	return d.Device.Close()
}

func TestCurrentWatchRetiresOnlyBrokenConnection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		drop bool
	}{
		{name: "ordinary", err: errors.New("store unavailable")},
		{name: "cancelled", err: context.Canceled},
		{name: "disconnected", err: device.ErrDisconnected, drop: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()

			var closes atomic.Int32

			mgr := watchsession.NewManager(func(context.Context) (device.Device, error) {
				dev, err := device.NewDirFS(root)
				return currentWatchDevice{Device: dev, closes: &closes}, err
			})

			t.Cleanup(func() { closeCurrentManager(t, mgr) })

			first, err := mgr.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			selected := first.Session()
			if err := first.Release(); err != nil {
				t.Fatal(err)
			}

			_, err = WithCurrentWatch(t.Context(), mgr, func(_ context.Context, lease *watchsession.Lease) (any, error) {
				if lease == nil || lease.Session() != selected {
					t.Fatal("optional helper changed the current Session")
				}

				return nil, test.err
			})
			if !errors.Is(err, test.err) || (closes.Load() == 1) != test.drop || (mgr.Current() == nil) != test.drop || mgr.Gate().Snapshot().Operations != 0 {
				t.Fatalf("error=%v closes=%d gate=%+v", err, closes.Load(), mgr.Gate().Snapshot())
			}
		})
	}
}

func closeCurrentManager(t *testing.T, mgr *watchsession.Manager) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
	defer cancel()

	if err := mgr.Close(ctx); err != nil {
		t.Error(err)
	}
}
