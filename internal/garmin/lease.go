package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Lease preserves feature access over one bounded Session lease.
type Lease struct {
	lease *watchsession.Lease
	svc   *Service
}

// Service is valid only until Release; do not retain it for another job group.
func (l *Lease) Service() *Service { return l.svc }

// Context observes caller, group, release and Manager cancellation.
func (l *Lease) Context() context.Context { return l.lease.Context() }

// Epoch binds later operation groups to this connection.
func (l *Lease) Epoch() ConnectionEpoch { return l.lease.Epoch() }

// Release joins retirement cleanup and releases the underlying admission once.
func (l *Lease) Release() error { return l.lease.Release() }

func (l *Lazy) legacyLease(lease *watchsession.Lease, err error) (*Lease, error) {
	if lease == nil {
		return nil, err
	}

	return &Lease{lease: lease, svc: l.facade(lease.Session(), lease.Epoch())}, err
}

// Acquire waits cancellably for one group, opening a session lazily if needed.
func (l *Lazy) Acquire(ctx context.Context) (*Lease, error) {
	return l.legacyLease(l.manager.Acquire(ctx))
}

// AcquireCurrent obtains a lease only if a session is already open.
func (l *Lazy) AcquireCurrent(ctx context.Context) (*Lease, error) {
	return l.legacyLease(l.manager.AcquireCurrent(ctx))
}

// AcquireAt rejects a changed/missing expected epoch without reopening it.
func (l *Lazy) AcquireAt(ctx context.Context, expected ConnectionEpoch) (*Lease, error) {
	return l.legacyLease(l.manager.AcquireAt(ctx, expected))
}
