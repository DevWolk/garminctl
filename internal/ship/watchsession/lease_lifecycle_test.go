package watchsession

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

type leaseDevice struct {
	reads         atomic.Int32
	closes        atomic.Int32
	aborts        atomic.Int32
	autoReconnect atomic.Bool
	closeErr      error
	readEntered   chan struct{}
	aborted       chan struct{}
	readOnce      sync.Once
	abortOnce     sync.Once
	closeEntered  chan struct{}
	closeRelease  chan struct{}
}

func (*leaseDevice) Info() device.Info                                    { return device.Info{ID: "lease-test"} }
func (*leaseDevice) List(context.Context, string) ([]device.Entry, error) { return nil, nil }
func (*leaseDevice) Stat(context.Context, string) (device.Entry, error) {
	return device.Entry{Name: "GARMIN", Dir: true}, nil
}

func (d *leaseDevice) Read(ctx context.Context, _ string) ([]byte, error) {
	d.reads.Add(1)

	if d.readEntered != nil {
		d.readOnce.Do(func() { close(d.readEntered) })

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-d.aborted:
			return nil, device.ErrDisconnected
		}
	}

	return []byte("sample"), nil
}
func (*leaseDevice) Write(context.Context, string, []byte) error { return nil }
func (*leaseDevice) Delete(context.Context, string) error        { return nil }
func (*leaseDevice) Mkdir(context.Context, string) error         { return nil }
func (d *leaseDevice) Close() error {
	d.closes.Add(1)

	if d.closeEntered != nil {
		close(d.closeEntered)
	}

	if d.closeRelease != nil {
		<-d.closeRelease
	}

	return d.closeErr
}
func (d *leaseDevice) SetAutoReconnect(enabled bool) { d.autoReconnect.Store(enabled) }
func (d *leaseDevice) Abort() {
	d.aborts.Add(1)

	if d.aborted != nil {
		d.abortOnce.Do(func() { close(d.aborted) })
	}
}

func TestLeaseQueueCancellationPreservesConnectionAndAdmission(t *testing.T) {
	t.Parallel()

	dev := &leaseDevice{}

	var opens atomic.Int32

	manager := NewManager(func(context.Context) (device.Device, error) { opens.Add(1); return dev, nil })

	t.Cleanup(func() { closeManager(t, manager) })
	first := acquireManagerLease(t, manager)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if _, err := manager.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("queued cancellation: %v", err)
	}

	if snapshot := manager.Gate().Snapshot(); snapshot.Operations != 1 {
		t.Fatalf("queued permit leaked: %+v", snapshot)
	}

	if dev.closes.Load() != 0 || dev.reads.Load() != 0 || opens.Load() != 1 {
		t.Fatal("cancelled waiter touched the transport")
	}

	if dev.autoReconnect.Load() {
		t.Fatal("managed connection permits mid-group reconnect")
	}

	epoch, session := first.Epoch(), first.Session()
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	next := acquireManagerLease(t, manager)
	if next.Epoch() != epoch || next.Session() != session || opens.Load() != 1 {
		t.Fatal("waiter cancellation replaced a healthy connection")
	}
}

func TestDropCancelsGroupClosesOnReleaseAndRejectsOldEpoch(t *testing.T) {
	t.Parallel()

	old := &leaseDevice{readEntered: make(chan struct{}), aborted: make(chan struct{})}
	fresh := &leaseDevice{}

	var opens atomic.Int32

	manager := NewManager(func(context.Context) (device.Device, error) {
		if opens.Add(1) == 1 {
			return old, nil
		}

		return fresh, nil
	})

	t.Cleanup(func() { closeManager(t, manager) })
	lease := acquireManagerLease(t, manager)
	epoch := lease.Epoch()
	readDone := make(chan error, 1)

	go func() { _, err := lease.Session().ReadFile(lease.Context(), "GARMIN/test"); readDone <- err }()

	<-old.readEntered

	if err := manager.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	if manager.Current() != nil || old.closes.Load() != 0 {
		t.Fatal("Drop closed an actively owned group or retained it as current")
	}

	if err := <-readDone; !errors.Is(err, context.Canceled) && !errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("dropped read: %v", err)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if old.closes.Load() != 1 {
		t.Fatal("retired connection was not closed by its owner")
	}

	if _, err := manager.AcquireAt(t.Context(), epoch); !errors.Is(err, ErrSessionChanged) || opens.Load() != 1 {
		t.Fatalf("old job reopened: %v opens=%d", err, opens.Load())
	}

	next := acquireManagerLease(t, manager)
	if next.Epoch() == epoch {
		t.Fatal("replacement reused epoch")
	}

	if err := manager.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	if manager.Current() != next.Session() || fresh.closes.Load() != 0 {
		t.Fatal("delayed old error dropped replacement")
	}
}

func TestCloseDeadlineLeavesOwnedCleanupAndLaterJoin(t *testing.T) {
	t.Parallel()

	dev := &leaseDevice{}
	manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })
	lease := acquireManagerLease(t, manager)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := manager.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close did not wait for held lease: %v", err)
	}

	if !errors.Is(lease.Context().Err(), context.Canceled) || dev.closes.Load() != 0 {
		t.Fatal("Close did not cancel ownership or closed it prematurely")
	}

	if _, err := manager.Acquire(t.Context()); !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("closed manager admitted work: %v", err)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	closeManager(t, manager)
	closeManager(t, manager)

	if dev.closes.Load() != 1 || dev.aborts.Load() != 1 {
		t.Fatalf("cleanup count closes=%d aborts=%d", dev.closes.Load(), dev.aborts.Load())
	}
}

func TestCloseCancelsOpenAndRejectsLatePublication(t *testing.T) {
	t.Parallel()

	entered, finish := make(chan struct{}), make(chan struct{})
	dev := &leaseDevice{}
	manager := NewManager(func(context.Context) (device.Device, error) { close(entered); <-finish; return dev, nil })
	opened := make(chan error, 1)

	go func() {
		lease, err := manager.Acquire(t.Context())
		if lease != nil {
			_ = lease.Release()
		}

		opened <- err
	}()

	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := manager.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending open Close: %v", err)
	}

	close(finish)

	if err := <-opened; !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("late open published: %v", err)
	}

	closeManager(t, manager)

	if manager.Current() != nil || dev.closes.Load() != 1 || manager.Gate().Snapshot().Operations != 0 {
		t.Fatal("late opened transport or admission leaked")
	}
}

func TestCloseDeadlineAlsoBoundsRetiredTransportCleanup(t *testing.T) {
	t.Parallel()

	dev := &leaseDevice{closeEntered: make(chan struct{}), closeRelease: make(chan struct{})}
	manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

	lease, err := manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := manager.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked device Close ignored caller deadline: %v", err)
	}

	<-dev.closeEntered
	close(dev.closeRelease)
	closeManager(t, manager)

	if dev.closes.Load() != 1 {
		t.Fatal("pending cleanup was started twice")
	}
}

func TestFailedRetiredCloseBlocksReplacement(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("synthetic close failure")
	dev := &leaseDevice{closeErr: closeErr}

	var opens atomic.Int32

	manager := NewManager(func(context.Context) (device.Device, error) { opens.Add(1); return dev, nil })

	lease, err := manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if err := manager.DropAt(epoch); !errors.Is(err, closeErr) {
		t.Fatalf("close error hidden: %v", err)
	}

	if _, err := manager.Acquire(t.Context()); !errors.Is(err, closeErr) || opens.Load() != 1 {
		t.Fatalf("unsafe replacement: %v opens=%d", err, opens.Load())
	}

	if err := manager.Close(t.Context()); !errors.Is(err, closeErr) {
		t.Fatalf("shutdown hid failed close: %v", err)
	}
}

func TestDropRacingReleaseCannotMissRetiredCleanup(t *testing.T) {
	t.Parallel()

	for range 100 {
		dev := &leaseDevice{}
		manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

		lease, err := manager.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)

		go func() { <-start; results <- lease.Release() }()
		go func() { <-start; results <- manager.DropAt(lease.Epoch()) }()

		close(start)

		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}

		if dev.closes.Load() != 1 {
			t.Fatalf("retired cleanup missed: closes=%d", dev.closes.Load())
		}

		closeManager(t, manager)
	}
}

type groupedLeaseDevice struct {
	leaseDevice
	mu     sync.Mutex
	events []string
}

func (d *groupedLeaseDevice) Read(context.Context, string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.events = append(d.events, "read current")

	return []byte("current"), nil
}

func (d *groupedLeaseDevice) Write(_ context.Context, path string, _ []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.events = append(d.events, path)

	return nil
}

func TestLeaseKeepsReadModifyWriteTogetherAgainstQueuedMutation(t *testing.T) {
	t.Parallel()

	dev := &groupedLeaseDevice{}
	manager := NewManager(func(context.Context) (device.Device, error) { return dev, nil })

	t.Cleanup(func() { closeManager(t, manager) })

	first := acquireManagerLease(t, manager)
	if _, err := first.Session().ReadFile(first.Context(), "OUT.BIN"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		second, err := manager.AcquireAt(t.Context(), first.Epoch())
		if err != nil {
			done <- err
			return
		}

		err = second.Session().WriteFile(second.Context(), "competing mutation", nil)
		done <- errors.Join(err, second.Release())
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for manager.Gate().Snapshot().Operations != 2 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("competing operation did not reserve its queued lease")
		}
	}

	if err := first.Session().WriteFile(first.Context(), "dependent IN.BIN", nil); err != nil {
		t.Fatal(err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	dev.mu.Lock()
	defer dev.mu.Unlock()

	if got := strings.Join(dev.events, " -> "); got != "read current -> dependent IN.BIN -> competing mutation" {
		t.Fatalf("dependent steps interleaved: %s", got)
	}
}
