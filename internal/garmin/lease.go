package garmin

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

// Lease owns one bounded exclusive operation group. Service and Context are
// valid only until Release; a background job acquires its own subsequent lease.
type Lease struct {
	owner            *Lazy
	svc              *Service
	epoch            ConnectionEpoch
	ctx              context.Context //nolint:containedctx // The lease owns this cancellation scope and releases it with its exclusive permit.
	cancel           context.CancelFunc
	releaseAdmission func()
	once             sync.Once
	err              error
}

// Service returns the session for this group; never retain it after Release.
func (l *Lease) Service() *Service { return l.svc }

// Context is cancelled by the caller, Release or manager Close/Drop. Device I/O
// in the group must use it so shutdown can interrupt an admitted transaction.
func (l *Lease) Context() context.Context { return l.ctx }

// Epoch binds later job groups to this exact connection.
func (l *Lease) Epoch() ConnectionEpoch { return l.epoch }

// Release ends ownership, closes any retired connection and releases admission.
// Concurrent or repeated calls return the same cleanup result.
func (l *Lease) Release() error {
	l.once.Do(func() {
		l.cancel()

		owner := l.owner
		for {
			l.err = errors.Join(l.err, owner.closeRetired())
			owner.mu.Lock()
			if owner.retired != nil {
				owner.mu.Unlock()
				continue
			}

			owner.active = nil
			if owner.svc != nil && l.svc != nil {
				owner.lastUse = time.Now()
			}
			// Drop observes either this owner or an available slot. There is no
			// gap in which a retirement can miss both cleanup paths.
			<-owner.slot
			owner.mu.Unlock()

			break
		}

		l.releaseAdmission()
	})

	return l.err
}

// Acquire waits cancellably for one operation group, opening lazily if needed.
func (l *Lazy) Acquire(ctx context.Context) (*Lease, error) { return l.AcquireAt(ctx, 0) }

// AcquireAt rejects a missing/changed expected connection without reopening.
// An expected epoch of zero permits an independent operation to open lazily.
func (l *Lazy) AcquireAt(ctx context.Context, expected ConnectionEpoch) (*Lease, error) {
	release, err := l.gate.Enter(ctx)
	if err != nil {
		return nil, err
	}

	select {
	case l.slot <- struct{}{}:
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-l.stopped:
		release()
		return nil, lifecycle.ErrStopped
	}

	return l.acquireReserved(ctx, expected, release)
}

// acquireReserved consumes one reserved admission permit and the held slot.
func (l *Lazy) acquireReserved(ctx context.Context, expected ConnectionEpoch, release func()) (*Lease, error) {
	// Bound one operation group independently of a long sync/decode job.
	// A shorter request/job deadline still wins. Release never cancels its parent.
	groupCtx, cancel := context.WithTimeout(ctx, l.operationLimit)
	lease := &Lease{owner: l, ctx: groupCtx, cancel: cancel, releaseAdmission: release}
	l.mu.Lock()
	if l.closed || ctx.Err() != nil {
		var stopped error
		if l.closed {
			stopped = lifecycle.ErrStopped
		}
		l.mu.Unlock()
		cancel()
		<-l.slot
		release()

		return nil, errors.Join(stopped, ctx.Err())
	}

	l.active = lease
	l.mu.Unlock()

	svc, epoch, err := l.connection(groupCtx, expected)
	if err != nil {
		return nil, errors.Join(err, lease.Release())
	}

	if err := l.bindLease(lease, svc, epoch); err != nil {
		return nil, errors.Join(err, lease.Release())
	}

	return lease, nil
}

func (l *Lazy) bindLease(lease *Lease, svc *Service, epoch ConnectionEpoch) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed || lease.ctx.Err() != nil || l.svc != svc || l.epoch != epoch {
		var stopped error
		if l.closed {
			stopped = lifecycle.ErrStopped
		}

		if l.svc != svc || l.epoch != epoch {
			stopped = errors.Join(stopped, ErrSessionChanged)
		}

		return errors.Join(stopped, lease.ctx.Err())
	}

	lease.svc, lease.epoch = svc, epoch

	return nil
}
