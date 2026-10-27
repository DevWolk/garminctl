package garmin

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

type runtimeOrderDevice struct {
	leaseDevice
	jobFinished     <-chan struct{}
	closedBeforeJob atomic.Bool
}

func (d *runtimeOrderDevice) Close() error {
	select {
	case <-d.jobFinished:
	default:
		d.closedBeforeJob.Store(true)
	}

	return d.leaseDevice.Close()
}

func TestRuntimeShutdownJoinsJobBeforeDeviceClose(t *testing.T) {
	t.Parallel()

	finished := make(chan struct{})
	dev := &runtimeOrderDevice{jobFinished: finished}
	runtime := NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return dev, nil })

	lease, err := runtime.Watch.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})

	job, _, err := runtime.Sync.Start(t.Context(), SyncOptions{Epoch: epoch}, func(ctx context.Context, _ Progress) (*SyncResult, error) {
		close(started)
		<-ctx.Done()
		close(finished)

		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	waitActivityBarrier(t, started)

	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := job.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("job error: %v", err)
	}

	if dev.closedBeforeJob.Load() || dev.closes.Load() != 1 {
		t.Fatal("device closed before job joined")
	}

	if snapshot := runtime.Watch.Gate().Snapshot(); snapshot.State != lifecycle.Closed || snapshot.Jobs != 0 || snapshot.Operations != 0 {
		t.Fatalf("runtime did not drain: %+v", snapshot)
	}
}

func TestRuntimeRequestCancellationAndStop(t *testing.T) {
	t.Parallel()
	runtime := NewRuntime(t.Context(), nil)
	client, cancelClient := context.WithCancel(t.Context())

	first, releaseFirst := runtime.RequestContext(client)
	defer releaseFirst()

	second, releaseSecond := runtime.RequestContext(t.Context())
	defer releaseSecond()

	cancelClient()

	if first.Err() == nil || second.Err() != nil {
		t.Fatal("one client cancelled another request")
	}

	runtime.Stop()

	select {
	case <-second.Done():
	case <-time.After(time.Second):
		t.Fatal("runtime stop did not cancel request")
	}

	if _, err := runtime.Watch.Acquire(t.Context()); !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("work admitted after stop: %v", err)
	}

	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeIncompleteShutdownRetainsJobOwnership(t *testing.T) {
	t.Parallel()
	runtime := NewRuntime(t.Context(), nil)
	gate := make(chan struct{})

	release := sync.OnceFunc(func() { close(gate) })
	defer release()

	started := make(chan struct{})

	_, _, err := runtime.Sync.Start(t.Context(), SyncOptions{}, func(context.Context, Progress) (*SyncResult, error) {
		close(started)
		<-gate

		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	waitActivityBarrier(t, started)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := runtime.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished shutdown: %v", err)
	}

	if runtime.Watch.Gate().Snapshot().Jobs != 1 {
		t.Fatal("unfinished job lost admission")
	}

	release()

	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireCurrentDoesNotOpenOptionalWatch(t *testing.T) {
	t.Parallel()

	var opens atomic.Int32

	watch := NewLazy(func(context.Context) (device.Device, error) { opens.Add(1); return &leaseDevice{}, nil })

	lease, err := watch.AcquireCurrent(t.Context())
	if err != nil || lease != nil || opens.Load() != 0 {
		t.Fatalf("optional request opened watch: %v, %v", lease, err)
	}

	initial, err := watch.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := initial.Epoch()
	if err := initial.Release(); err != nil {
		t.Fatal(err)
	}

	lease, err = watch.AcquireCurrent(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if lease.Epoch() != epoch || opens.Load() != 1 {
		t.Fatal("optional access changed epoch")
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if err := watch.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
