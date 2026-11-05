package watchsession

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

// Opener connects to the device on demand. It bounds its own waiting.
type Opener func(ctx context.Context) (device.Device, error)

// ConnectionEpoch identifies one managed connection, independently of cache
// generations. A job must not continue on another epoch after reconnect/Drop.
type ConnectionEpoch uint64

// ErrSessionChanged means that a job's original connection is no longer current.
var ErrSessionChanged = errors.New("watch connection changed; restart the operation")

// Manager owns one connection and exclusive bounded operation leases. Its state
// mutex never covers opening, device I/O, close or cancellation callbacks.
type Manager struct {
	open           Opener
	gate           *lifecycle.Gate
	slot           chan struct{}
	stopped        chan struct{}
	mu             sync.Mutex
	session        *Session
	retired        *Session
	cleanupErr     error
	epoch          ConnectionEpoch
	epochDone      chan struct{}
	active         *Lease
	closed         bool
	lastUse        time.Time
	operationLimit time.Duration
	closeOnce      sync.Once
	closeDone      chan struct{}
	closeErr       error
}

// NewManager creates a standalone owner. Shared runtimes inject their single Gate
// through NewManagerWithGate.
func NewManager(open Opener) *Manager { return NewManagerWithGate(open, new(lifecycle.Gate)) }

// NewManagerWithGate shares operation/job/maintenance admission with the runtime.
func NewManagerWithGate(open Opener, gate *lifecycle.Gate) *Manager {
	return &Manager{
		open: open, gate: gate,
		slot: make(chan struct{}, 1), stopped: make(chan struct{}), closeDone: make(chan struct{}),
		operationLimit: 10 * time.Minute,
	}
}

// Gate is the same admission owner used by this Manager's queued operations.
func (l *Manager) Gate() *lifecycle.Gate { return l.gate }

// CurrentEpoch is a pure connection snapshot; zero means no open connection.
func (l *Manager) CurrentEpoch() ConnectionEpoch {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed || l.session == nil {
		return 0
	}

	return l.epoch
}

// Snapshot returns one atomic connection/epoch observation without a lease.
// Use Acquire before I/O. A closed or unopened manager returns nil and zero.
func (l *Manager) Snapshot() (*Session, ConnectionEpoch) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed || l.session == nil {
		return nil, 0
	}

	return l.session, l.epoch
}

// Current is an unleased connection snapshot. I/O callers must Acquire instead.
func (l *Manager) Current() *Session {
	session, _ := l.Snapshot()
	return session
}

// Drop retires the current connection without a lease, as an explicit
// disconnect request does. Leased callers use DropAt so a delayed error cannot
// drop a newer connection.
func (l *Manager) Drop() {
	l.mu.Lock()
	epoch := l.epoch
	l.mu.Unlock()
	_ = l.DropAt(epoch)
}

// DropAt invalidates only the matching connection. An active group is cancelled
// and closes its retired transport when it releases; no close waits on itself.
func (l *Manager) DropAt(epoch ConnectionEpoch) error {
	l.mu.Lock()
	if l.session == nil || l.epoch != epoch {
		l.mu.Unlock()
		return nil
	}

	retired := l.session
	l.retired, l.session = retired, nil
	l.epoch++
	close(l.epochDone)
	l.epochDone = nil
	active := l.active
	l.mu.Unlock()

	if active != nil {
		active.cancel()
		retired.Abort()

		return nil
	}

	select {
	case l.slot <- struct{}{}:
		defer func() { <-l.slot }()
		return l.closeRetired()
	default:
		return nil // The admitted owner closes it before opening/releasing.
	}
}

// closeRetired runs only while holding the exclusive slot, outside mu. A failed
// cleanup latches a fault: opening another transport could create two owners.
func (l *Manager) closeRetired() error {
	l.mu.Lock()
	retired, prior := l.retired, l.cleanupErr
	l.retired = nil
	l.mu.Unlock()

	if retired == nil {
		return prior
	}

	err := retired.Close()

	l.mu.Lock()
	l.cleanupErr = errors.Join(l.cleanupErr, err)
	err = l.cleanupErr
	l.mu.Unlock()

	return err
}

func (l *Manager) connection(ctx context.Context, expected ConnectionEpoch) (*Session, ConnectionEpoch, error) {
	if err := l.closeRetired(); err != nil {
		return nil, 0, fmt.Errorf("watch cleanup failed: %w", err)
	}

	l.mu.Lock()
	if expected != 0 && (l.session == nil || l.epoch != expected) {
		l.mu.Unlock()
		return nil, 0, errors.Join(ErrSessionChanged, device.ErrDisconnected)
	}

	if l.session != nil {
		session, epoch := l.session, l.epoch
		l.mu.Unlock()

		return session, epoch, nil
	}
	l.mu.Unlock()

	return l.openConnection(ctx)
}

func (l *Manager) openConnection(ctx context.Context) (*Session, ConnectionEpoch, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}

	dev, err := l.open(ctx)
	if err != nil {
		return nil, 0, err
	}

	if policy, ok := dev.(device.ReconnectPolicy); ok {
		policy.SetAutoReconnect(false)
	}

	session := New(dev)

	l.mu.Lock()
	if l.closed || ctx.Err() != nil {
		var stopped error
		if l.closed {
			stopped = lifecycle.ErrStopped
		}
		l.mu.Unlock()

		closeErr := dev.Close()

		l.mu.Lock()
		l.cleanupErr = errors.Join(l.cleanupErr, closeErr)
		l.mu.Unlock()

		return nil, 0, errors.Join(stopped, ctx.Err(), closeErr)
	}

	l.epoch++
	l.epochDone = make(chan struct{})
	l.session = session
	epoch := l.epoch
	l.mu.Unlock()

	return session, epoch, nil
}

// Close stops this owner, cancels its active group/open and joins ownership
// before closing. A deadline reports incomplete cleanup; a later Close can join
// again. It does not stop the runtime's shared Gate or other resource owners.
func (l *Manager) Close(ctx context.Context) error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		close(l.stopped)

		if l.session != nil {
			l.retired, l.session = l.session, nil
			l.epoch++
			close(l.epochDone)
			l.epochDone = nil
		}

		active, retired := l.active, l.retired
		l.mu.Unlock()

		if active != nil {
			active.cancel()
		}
		// This worker owns pending cleanup even when a caller's deadline expires.
		// Later Close calls join this same worker rather than closing twice.
		go func() {
			if active != nil {
				if retired != nil {
					retired.Abort()
				}
			}

			l.slot <- struct{}{}

			l.closeErr = l.closeRetired()
			<-l.slot
			close(l.closeDone)
		}()
	})

	select {
	case <-l.closeDone:
		return l.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
