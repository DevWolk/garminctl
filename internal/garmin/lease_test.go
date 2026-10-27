package garmin

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

func acquireLease(t *testing.T, l *Lazy) *Lease {
	t.Helper()

	lease, err := l.Acquire(t.Context())
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

func closeOwner(t *testing.T, l *Lazy) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.Close(ctx); err != nil {
		t.Error(err)
	}
}

func TestLeaseQueueCancellationPreservesConnectionAndAdmission(t *testing.T) {
	t.Parallel()

	dev := &leaseDevice{}

	var opens atomic.Int32

	l := NewLazy(func(context.Context) (device.Device, error) { opens.Add(1); return dev, nil })

	t.Cleanup(func() { closeOwner(t, l) })
	first := acquireLease(t, l)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if _, err := l.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("queued cancellation: %v", err)
	}

	if snapshot := l.Gate().Snapshot(); snapshot.Operations != 1 {
		t.Fatalf("queued permit leaked: %+v", snapshot)
	}

	if dev.closes.Load() != 0 || dev.reads.Load() != 0 || opens.Load() != 1 {
		t.Fatal("cancelled waiter touched the transport")
	}

	if dev.autoReconnect.Load() {
		t.Fatal("managed connection permits mid-group reconnect")
	}

	epoch, svc := first.Epoch(), first.Service()
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	next := acquireLease(t, l)
	if next.Epoch() != epoch || next.Service() != svc || opens.Load() != 1 {
		t.Fatal("waiter cancellation replaced a healthy connection")
	}
}

func TestDropCancelsGroupClosesOnReleaseAndRejectsOldEpoch(t *testing.T) {
	t.Parallel()

	old := &leaseDevice{readEntered: make(chan struct{}), aborted: make(chan struct{})}
	fresh := &leaseDevice{}

	var opens atomic.Int32

	l := NewLazy(func(context.Context) (device.Device, error) {
		if opens.Add(1) == 1 {
			return old, nil
		}

		return fresh, nil
	})

	t.Cleanup(func() { closeOwner(t, l) })
	lease := acquireLease(t, l)
	epoch := lease.Epoch()
	readDone := make(chan error, 1)

	go func() { _, err := lease.Service().ReadFile(lease.Context(), "GARMIN/test"); readDone <- err }()

	<-old.readEntered

	if err := l.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	if l.Current() != nil || old.closes.Load() != 0 {
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

	if _, err := l.AcquireAt(t.Context(), epoch); !errors.Is(err, ErrSessionChanged) || opens.Load() != 1 {
		t.Fatalf("old job reopened: %v opens=%d", err, opens.Load())
	}

	next := acquireLease(t, l)
	if next.Epoch() == epoch {
		t.Fatal("replacement reused epoch")
	}

	if err := l.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	if l.Current() != next.Service() || fresh.closes.Load() != 0 {
		t.Fatal("delayed old error dropped replacement")
	}
}

func TestCloseDeadlineLeavesOwnedCleanupAndLaterJoin(t *testing.T) {
	t.Parallel()

	dev := &leaseDevice{}
	l := NewLazy(func(context.Context) (device.Device, error) { return dev, nil })
	lease := acquireLease(t, l)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := l.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close did not wait for held lease: %v", err)
	}

	if !errors.Is(lease.Context().Err(), context.Canceled) || dev.closes.Load() != 0 {
		t.Fatal("Close did not cancel ownership or closed it prematurely")
	}

	if _, err := l.Acquire(t.Context()); !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("closed manager admitted work: %v", err)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	closeOwner(t, l)
	closeOwner(t, l)

	if dev.closes.Load() != 1 || dev.aborts.Load() != 1 {
		t.Fatalf("cleanup count closes=%d aborts=%d", dev.closes.Load(), dev.aborts.Load())
	}
}

func TestLeaseGroupDeadlineDoesNotCancelParentOrDropUnusedSession(t *testing.T) {
	t.Parallel()

	dev := &leaseDevice{}
	l := NewLazy(func(context.Context) (device.Device, error) { return dev, nil })

	warm := acquireLease(t, l)
	if err := warm.Release(); err != nil {
		t.Fatal(err)
	}

	l.operationLimit = 50 * time.Millisecond

	t.Cleanup(func() { closeOwner(t, l) })

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

	if dev.closes.Load() != 0 || l.Current() != lease.Service() {
		t.Fatal("pre-I/O deadline dropped unused healthy session")
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	next := acquireLease(t, l)
	if next.Epoch() != epoch {
		t.Fatal("unused group deadline replaced connection")
	}
}

func TestCloseCancelsOpenAndRejectsLatePublication(t *testing.T) {
	t.Parallel()

	entered, finish := make(chan struct{}), make(chan struct{})
	dev := &leaseDevice{}
	l := NewLazy(func(context.Context) (device.Device, error) { close(entered); <-finish; return dev, nil })
	opened := make(chan error, 1)

	go func() {
		lease, err := l.Acquire(t.Context())
		if lease != nil {
			_ = lease.Release()
		}

		opened <- err
	}()

	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := l.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending open Close: %v", err)
	}

	close(finish)

	if err := <-opened; !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("late open published: %v", err)
	}

	closeOwner(t, l)

	if l.Current() != nil || dev.closes.Load() != 1 || l.Gate().Snapshot().Operations != 0 {
		t.Fatal("late opened transport or admission leaked")
	}
}

func TestCloseDeadlineAlsoBoundsRetiredTransportCleanup(t *testing.T) {
	t.Parallel()

	dev := &leaseDevice{closeEntered: make(chan struct{}), closeRelease: make(chan struct{})}
	l := NewLazy(func(context.Context) (device.Device, error) { return dev, nil })

	lease, err := l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := l.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked device Close ignored caller deadline: %v", err)
	}

	<-dev.closeEntered
	close(dev.closeRelease)
	closeOwner(t, l)

	if dev.closes.Load() != 1 {
		t.Fatal("pending cleanup was started twice")
	}
}

func TestFailedRetiredCloseBlocksReplacement(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("synthetic close failure")
	dev := &leaseDevice{closeErr: closeErr}

	var opens atomic.Int32

	l := NewLazy(func(context.Context) (device.Device, error) { opens.Add(1); return dev, nil })

	lease, err := l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if err := l.DropAt(epoch); !errors.Is(err, closeErr) {
		t.Fatalf("close error hidden: %v", err)
	}

	if _, err := l.Acquire(t.Context()); !errors.Is(err, closeErr) || opens.Load() != 1 {
		t.Fatalf("unsafe replacement: %v opens=%d", err, opens.Load())
	}

	if err := l.Close(t.Context()); !errors.Is(err, closeErr) {
		t.Fatalf("shutdown hid failed close: %v", err)
	}
}

func TestDropRacingReleaseCannotMissRetiredCleanup(t *testing.T) {
	t.Parallel()

	for range 100 {
		dev := &leaseDevice{}
		l := NewLazy(func(context.Context) (device.Device, error) { return dev, nil })

		lease, err := l.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)

		go func() { <-start; results <- lease.Release() }()
		go func() { <-start; results <- l.DropAt(lease.Epoch()) }()

		close(start)

		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}

		if dev.closes.Load() != 1 {
			t.Fatalf("retired cleanup missed: closes=%d", dev.closes.Load())
		}

		closeOwner(t, l)
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
	l := NewLazy(func(context.Context) (device.Device, error) { return dev, nil })

	t.Cleanup(func() { closeOwner(t, l) })

	first := acquireLease(t, l)
	if _, err := first.Service().ReadFile(first.Context(), "OUT.BIN"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		second, err := l.AcquireAt(t.Context(), first.Epoch())
		if err != nil {
			done <- err
			return
		}

		err = second.Service().WriteFile(second.Context(), "competing mutation", nil)
		done <- errors.Join(err, second.Release())
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for l.Gate().Snapshot().Operations != 2 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("competing operation did not reserve its queued lease")
		}
	}

	if err := first.Service().WriteFile(first.Context(), "dependent IN.BIN", nil); err != nil {
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
