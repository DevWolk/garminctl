package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestRunSessionSharesLegacyOwnerAndAdmission(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	opens := 0
	runtime := garmin.NewRuntime(t.Context(), func(context.Context) (device.Device, error) {
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

	legacy, err := runtime.Watch.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	expected, epoch := runtime.Watch.Manager().Snapshot()

	if err := legacy.Release(); err != nil {
		t.Fatal(err)
	}

	bodyErr := errors.New("command failed")

	var released <-chan struct{}

	c := &cli{runtime: runtime}

	err = c.runSession(t.Context(), func(ctx context.Context, sess *watchsession.Session, _ []string) error {
		released = ctx.Done()

		if sess != expected || runtime.Watch.Manager().CurrentEpoch() != epoch {
			t.Fatal("extracted command acquired a different connection")
		}

		if resume, err := runtime.Watch.Gate().TryMaintenance(ctx); !errors.Is(err, lifecycle.ErrBusy) {
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

	resume, err := runtime.Watch.Gate().TryMaintenance(t.Context())
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

	runtime := garmin.NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return dev, nil })
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

	if sess, _ := runtime.Watch.Manager().Snapshot(); sess != nil {
		t.Fatal("disconnected Session remained current")
	}

	if _, err := dev.Stat(t.Context(), "GARMIN"); err == nil {
		t.Fatal("command returned before the retired transport closed")
	}
}
