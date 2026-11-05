package process

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

// orderDevice records whether the watch closed before the sync job finished.
type orderDevice struct {
	device.Device
	jobFinished     <-chan struct{}
	closedBeforeJob atomic.Bool
	closes          atomic.Int32
}

func (d *orderDevice) Close() error {
	select {
	case <-d.jobFinished:
	default:
		d.closedBeforeJob.Store(true)
	}

	d.closes.Add(1)

	return d.Device.Close()
}

func waitBarrier(t *testing.T, reached <-chan struct{}) {
	t.Helper()

	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("barrier was not reached")
	}
}

func openDir(t *testing.T) func(context.Context) (device.Device, error) {
	t.Helper()

	root := t.TempDir()

	return func(context.Context) (device.Device, error) { return device.NewDirFS(root) }
}

func TestRuntimeShutdownJoinsJobBeforeDeviceClose(t *testing.T) {
	t.Parallel()

	finished := make(chan struct{})
	root := t.TempDir()
	dev := &orderDevice{jobFinished: finished}
	runtime := New(t.Context(), func(context.Context) (device.Device, error) {
		inner, err := device.NewDirFS(root)
		dev.Device = inner

		return dev, err
	})

	lease, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})

	job, _, err := runtime.Sync.Start(t.Context(), connect.SyncOptions{Epoch: epoch}, func(ctx context.Context, _ connect.Progress) (*connect.SyncResult, error) {
		close(started)
		<-ctx.Done()
		close(finished)

		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	waitBarrier(t, started)

	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := job.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("job error: %v", err)
	}

	if dev.closedBeforeJob.Load() || dev.closes.Load() != 1 {
		t.Fatal("device closed before job joined")
	}

	if snapshot := runtime.Manager().Gate().Snapshot(); snapshot.State != lifecycle.Closed || snapshot.Jobs != 0 || snapshot.Operations != 0 {
		t.Fatalf("runtime did not drain: %+v", snapshot)
	}
}

func TestRuntimeRequestCancellationAndStop(t *testing.T) {
	t.Parallel()

	runtime := New(t.Context(), nil)
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

	if _, err := runtime.Manager().Acquire(t.Context()); !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("work admitted after stop: %v", err)
	}

	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeIncompleteShutdownRetainsJobOwnership(t *testing.T) {
	t.Parallel()

	runtime := New(t.Context(), nil)
	gate := make(chan struct{})

	release := sync.OnceFunc(func() { close(gate) })
	defer release()

	started := make(chan struct{})

	_, _, err := runtime.Sync.Start(t.Context(), connect.SyncOptions{}, func(context.Context, connect.Progress) (*connect.SyncResult, error) {
		close(started)
		<-gate

		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	waitBarrier(t, started)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := runtime.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished shutdown: %v", err)
	}

	if runtime.Manager().Gate().Snapshot().Jobs != 1 {
		t.Fatal("unfinished job lost admission")
	}

	release()

	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeKeepsOneSchemaCachePerConnection(t *testing.T) {
	t.Parallel()

	runtime := New(t.Context(), openDir(t))
	t.Cleanup(func() {
		if err := runtime.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})

	lease, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	firstSession, firstEpoch := lease.Session(), lease.Epoch()

	cache := runtime.AppSchemaCacheFor(firstSession)
	if cache != runtime.AppSchemaCacheFor(firstSession) {
		t.Fatal("one connection got two schema caches")
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if err := runtime.Manager().DropAt(firstEpoch); err != nil {
		t.Fatal(err)
	}

	next, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := next.Release(); err != nil {
			t.Error(err)
		}
	}()

	fresh := runtime.AppSchemaCacheFor(next.Session())
	if fresh == cache {
		t.Fatal("new connection reused the previous connection's schema cache")
	}

	if late := runtime.AppSchemaCacheFor(firstSession); late == fresh || late == cache || runtime.AppSchemaCacheFor(next.Session()) != fresh {
		t.Fatal("late lookup with the retired Session displaced or shared the current cache")
	}
}

func TestRuntimeCloseDropsSchemaCache(t *testing.T) {
	t.Parallel()

	runtime := New(t.Context(), openDir(t))

	lease, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	session := lease.Session()
	cache := runtime.AppSchemaCacheFor(session)

	if err := errors.Join(lease.Release(), runtime.Close(t.Context())); err != nil {
		t.Fatal(err)
	}

	if runtime.AppSchemaCacheFor(session) == cache {
		t.Fatal("closed runtime still serves the connection's schema cache")
	}

	claims := runtime.MessageClaims()
	if claims == nil || claims != runtime.MessageClaims() {
		t.Fatal("runtime handed out two claim registries")
	}
}
