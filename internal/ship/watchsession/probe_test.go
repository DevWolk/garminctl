package watchsession

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

type idleProbeDevice struct {
	leaseDevice
	stats atomic.Int32
	stat  func(context.Context) error
}

func (d *idleProbeDevice) Stat(ctx context.Context, _ string) (device.Entry, error) {
	d.stats.Add(1)

	if d.stat != nil {
		return device.Entry{}, d.stat(ctx)
	}

	return device.Entry{Name: "GARMIN", Dir: true}, nil
}

func TestIdleProbeSkipsHeldLeaseWithoutTouchingDevice(t *testing.T) {
	t.Parallel()

	dev := &idleProbeDevice{}
	manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

	t.Cleanup(func() { closeManager(t, manager) })

	lease := acquireManagerLease(t, manager)
	for range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		observation, err := manager.ProbeIfIdle(ctx, 0)

		cancel()

		if observation != ProbeSkipped || err != nil || dev.stats.Load() != 0 {
			t.Fatalf("busy probe did I/O: observation=%v err=%v stats=%d", observation, err, dev.stats.Load())
		}
	}

	if manager.Current() != lease.Session() || dev.closes.Load() != 0 {
		t.Fatal("busy probe dropped live connection")
	}
}

func TestIdleProbeDoesNotOpenOrInventConnectionDuringRecentWork(t *testing.T) {
	t.Parallel()

	dev := &idleProbeDevice{}

	var opens atomic.Int32

	manager := NewManager(func(context.Context) (device.Device, error) { opens.Add(1); return dev, nil })

	t.Cleanup(func() { closeManager(t, manager) })

	if observation, err := manager.ProbeIfIdle(t.Context(), 0); observation != ProbeDisconnected || err != nil || opens.Load() != 0 {
		t.Fatalf("probe opened missing session: %v %v", observation, err)
	}

	lease := acquireManagerLease(t, manager)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if observation, err := manager.ProbeIfIdle(t.Context(), time.Hour); observation != ProbeSkipped || err != nil || dev.stats.Load() != 0 {
		t.Fatalf("recent operation probe: %v %v", observation, err)
	}

	if observation, err := manager.ProbeIfIdle(t.Context(), 0); observation != ProbeConnected || err != nil || dev.stats.Load() != 1 {
		t.Fatalf("idle observation: %v %v", observation, err)
	}
}

func TestIdleProbeClassifiesErrorsAndClosesOnlyBrokenEpoch(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		err    error
		want   ProbeObservation
		closed int32
	}{
		{name: "busy", err: device.ErrBusy, want: ProbeSkipped},
		{name: "deadline before I/O", err: context.DeadlineExceeded, want: ProbeSkipped},
		{name: "not found", err: device.ErrNotFound, want: ProbeSkipped},
		{name: "broken", err: errors.Join(device.ErrDisconnected, context.DeadlineExceeded), want: ProbeDisconnected, closed: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dev := &idleProbeDevice{stat: func(context.Context) error { return tt.err }}
			manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

			t.Cleanup(func() { closeManager(t, manager) })

			lease := acquireManagerLease(t, manager)
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}

			observation, err := manager.ProbeIfIdle(t.Context(), 0)
			if observation != tt.want || !errors.Is(err, tt.err) || dev.closes.Load() != tt.closed {
				t.Fatalf("probe classification: observation=%v err=%v closes=%d", observation, err, dev.closes.Load())
			}
		})
	}
}

func TestIdleProbeSkipsMaintenanceButMayObserveBetweenJobOperations(t *testing.T) {
	t.Parallel()

	dev := &idleProbeDevice{}
	manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

	t.Cleanup(func() { closeManager(t, manager) })

	lease := acquireManagerLease(t, manager)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	resume, err := manager.Gate().TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if observation, err := manager.ProbeIfIdle(t.Context(), 0); observation != ProbeSkipped || err == nil || dev.stats.Load() != 0 {
		t.Fatalf("probe entered maintenance: %v %v", observation, err)
	}

	resume()

	releaseJob, err := manager.Gate().EnterJob(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseJob()

	if observation, err := manager.ProbeIfIdle(t.Context(), 0); observation != ProbeConnected || err != nil || dev.stats.Load() != 1 {
		t.Fatalf("job activity substituted for real observation: %v %v", observation, err)
	}
}

func TestIdleProbeDoesNotConfirmEpochDroppedDuringStat(t *testing.T) {
	t.Parallel()

	entered, release := make(chan struct{}), make(chan struct{})
	dev := &idleProbeDevice{stat: func(context.Context) error { close(entered); <-release; return nil }}
	manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

	t.Cleanup(func() { closeManager(t, manager) })
	lease := acquireManagerLease(t, manager)

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	result := make(chan ProbeObservation, 1)

	go func() { observation, _ := manager.ProbeIfIdle(t.Context(), 0); result <- observation }()

	<-entered

	if err := manager.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	close(release)

	if observation := <-result; observation != ProbeDisconnected {
		t.Fatalf("retired epoch invented connection: %v", observation)
	}
}
