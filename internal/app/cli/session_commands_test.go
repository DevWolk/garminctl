package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestRunSessionSharesRuntimeOwnerAndAdmission(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	opens := 0
	runtime := app.NewRuntime(t.Context(), func(context.Context) (device.Device, error) {
		opens++
		return dev, nil
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	first, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	expected, epoch := runtime.Manager().Snapshot()

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	bodyErr := errors.New("command failed")

	var released <-chan struct{}

	c := &cli{runtime: runtime}

	err = c.runSession(t.Context(), func(ctx context.Context, sess *watchsession.Session, _ []string) error {
		released = ctx.Done()

		if sess != expected || runtime.Manager().CurrentEpoch() != epoch {
			t.Fatal("extracted command acquired a different connection")
		}

		if resume, err := runtime.Manager().Gate().TryMaintenance(ctx); !errors.Is(err, lifecycle.ErrBusy) {
			if resume != nil {
				resume()
			}

			t.Fatalf("maintenance during command = %v", err)
		}

		return bodyErr
	}, nil)
	if !errors.Is(err, bodyErr) || opens != 1 {
		t.Fatalf("command error=%v opens=%d", err, opens)
	}

	select {
	case <-released:
	default:
		t.Fatal("command lease context survived its release")
	}

	resume, err := runtime.Manager().Gate().TryMaintenance(t.Context())
	if err != nil {
		t.Fatalf("command left admission reserved: %v", err)
	}

	resume()
}

func TestRunSessionRetiresDisconnectedConnection(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	runtime := app.NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return dev, nil })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	c := &cli{runtime: runtime}

	err = c.runSession(t.Context(), func(context.Context, *watchsession.Session, []string) error {
		return device.ErrDisconnected
	}, nil)
	if !errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("disconnect cause lost: %v", err)
	}

	if sess, _ := runtime.Manager().Snapshot(); sess != nil {
		t.Fatal("disconnected Session remained current")
	}

	if _, err := dev.Stat(t.Context(), "GARMIN"); err == nil {
		t.Fatal("command returned before the retired transport closed")
	}
}
