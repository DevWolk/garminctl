package garmin

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

// Lazy owns one connection and exclusive bounded operation leases. Its state
// mutex never covers opening, device I/O, close or cancellation callbacks.
type Lazy struct {
	open       Opener
	gate       *lifecycle.Gate
	slot       chan struct{}
	stopped    chan struct{}
	mu         sync.Mutex
	svc        *Service
	retired    *Service
	cleanupErr error
	epoch      ConnectionEpoch
	active     *Lease
	closed     bool
	lastUse    time.Time
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
}

// NewLazy creates a standalone owner. Shared runtimes inject their single Gate
// through NewLazyWithGate.
func NewLazy(open Opener) *Lazy { return NewLazyWithGate(open, new(lifecycle.Gate)) }

// NewLazyWithGate shares operation/job/maintenance admission with the runtime.
func NewLazyWithGate(open Opener, gate *lifecycle.Gate) *Lazy {
	return &Lazy{open: open, gate: gate, slot: make(chan struct{}, 1), stopped: make(chan struct{}), closeDone: make(chan struct{})}
}

// Gate is the same admission owner used by this Manager's queued operations.
func (l *Lazy) Gate() *lifecycle.Gate { return l.gate }

// Current is a legacy snapshot. A returned Service is not an operation lease.
// New I/O callers must Acquire and release a Lease instead.
func (l *Lazy) Current() *Service {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	return l.svc
}

// Get is a temporary compatibility bridge for callers migrated in INV-04.a.3.
// It serializes opening but cannot reserve the caller's later use of Service.
func (l *Lazy) Get(ctx context.Context) (*Service, error) {
	lease, err := l.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	svc := lease.Service()

	return svc, lease.Release()
}

// Drop is the legacy current-connection invalidation bridge. Leased callers use
// DropAt so a delayed error cannot drop a newer connection.
func (l *Lazy) Drop() {
	l.mu.Lock()
	epoch := l.epoch
	l.mu.Unlock()
	_ = l.DropAt(epoch)
}

// DropAt invalidates only the matching connection. An active group is cancelled
// and closes its retired transport when it releases; no close waits on itself.
func (l *Lazy) DropAt(epoch ConnectionEpoch) error {
	l.mu.Lock()
	if l.svc == nil || l.epoch != epoch {
		l.mu.Unlock()
		return nil
	}

	retired := l.svc
	l.retired, l.svc = retired, nil
	l.epoch++
	active := l.active
	l.mu.Unlock()

	if active != nil {
		active.cancel()
		abortDevice(retired.dev)

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

func abortDevice(dev device.Device) {
	if aborter, ok := dev.(device.Aborter); ok {
		aborter.Abort()
	}
}

// closeRetired runs only while holding the exclusive slot, outside mu. A failed
// cleanup latches a fault: opening another transport could create two owners.
func (l *Lazy) closeRetired() error {
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

func (l *Lazy) connection(ctx context.Context, expected ConnectionEpoch) (*Service, ConnectionEpoch, error) {
	if err := l.closeRetired(); err != nil {
		return nil, 0, fmt.Errorf("watch cleanup failed: %w", err)
	}

	l.mu.Lock()
	if expected != 0 && (l.svc == nil || l.epoch != expected) {
		l.mu.Unlock()
		return nil, 0, errors.Join(ErrSessionChanged, device.ErrDisconnected)
	}

	if l.svc != nil {
		svc, epoch := l.svc, l.epoch
		l.mu.Unlock()

		return svc, epoch, nil
	}
	l.mu.Unlock()

	return l.openConnection(ctx)
}

func (l *Lazy) openConnection(ctx context.Context) (*Service, ConnectionEpoch, error) {
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

	svc := New(dev)

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
	l.svc = svc
	epoch := l.epoch
	l.mu.Unlock()

	return svc, epoch, nil
}

// Close stops this owner, cancels its active group/open and joins ownership
// before closing. A deadline reports incomplete cleanup; a later Close can join
// again. It does not stop the runtime's shared Gate or other resource owners.
func (l *Lazy) Close(ctx context.Context) error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		close(l.stopped)

		if l.svc != nil {
			l.retired, l.svc = l.svc, nil
			l.epoch++
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
					abortDevice(retired.dev)
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
