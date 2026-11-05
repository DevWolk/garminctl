package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type backupGroupWatch struct {
	device.Device
	check func()
}

func (d backupGroupWatch) List(ctx context.Context, path string) ([]device.Entry, error) {
	d.check()
	return d.Device.List(ctx, path)
}

func (d backupGroupWatch) Read(ctx context.Context, path string) ([]byte, error) {
	d.check()
	return d.Device.Read(ctx, path)
}

func TestBackupRetainsJobWhileReleasingEachDeviceGroup(t *testing.T) {
	t.Parallel()

	dev, _ := testdump.Watch(t)
	gate := new(lifecycle.Gate)
	operations := 0
	wrapped := backupGroupWatch{Device: dev, check: func() {
		operations++

		state := gate.Snapshot()
		if state.Operations != 1 || state.Jobs != 1 {
			t.Errorf("backup I/O admission=%+v", state)
		}
	}}
	runtime := app.NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return wrapped, nil })
	gate = runtime.Manager().Gate()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	lease, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	c := &cli{runtime: runtime, epoch: lease.Epoch()}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	copied := 0
	observer := watch.BackupObserver{Copied: func(string, int) {
		copied++

		state := gate.Snapshot()
		if state.Operations != 0 || state.Jobs != 1 {
			t.Errorf("host work retained device lease or lost job: %+v", state)
		}

		if resume, err := gate.TryMaintenance(t.Context()); !errors.Is(err, lifecycle.ErrBusy) {
			if resume != nil {
				resume()
			}

			t.Errorf("maintenance during backup job=%v", err)
		}
	}}

	err = c.runDeviceJob(t.Context(), "backup", func(ctx context.Context, _ []string) error {
		_, err := watch.NewBackupAction(&backupSource{cli: c}, observer).Run(ctx, watch.BackupRequest{Destination: t.TempDir()})
		return err
	}, nil)
	if err != nil || copied == 0 || operations <= copied {
		t.Fatalf("backup error=%v copied=%d device operations=%d", err, copied, operations)
	}

	state := gate.Snapshot()
	if state.Operations != 0 || state.Jobs != 0 {
		t.Fatalf("backup leaked admission: %+v", state)
	}
}
