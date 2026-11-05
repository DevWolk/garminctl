// Package process owns the shared watch and jobs of one backend process.
// Composition in internal/app builds it; shells receive it through their own
// narrow interfaces.
package process

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Runtime owns one process's watch Manager, its sync and activity jobs, the
// Connect message claims and the app schema cache of the current connection.
type Runtime struct {
	manager    *watchsession.Manager
	Sync       *connect.SyncCoordinator
	Activities *health.ActivityCoordinator
	claims     connect.MessageClaims
	schemas    apps.SessionSchemas
	ctx        context.Context //nolint:containedctx // The runtime owns, cancels and joins this lifetime, never a request lifetime.
	cancel     context.CancelFunc
}

// New constructs one admission gate for all watch operations and jobs.
func New(parent context.Context, open watchsession.Opener) *Runtime {
	ctx, cancel := context.WithCancel(parent)
	manager := watchsession.NewManagerWithGate(open, new(lifecycle.Gate))

	return &Runtime{
		manager:    manager,
		Sync:       connect.NewSyncCoordinator(ctx, manager.Gate()),
		Activities: health.NewActivityCoordinator(ctx, manager, fit.MustProfile()),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Manager is the runtime's watch owner.
func (r *Runtime) Manager() *watchsession.Manager { return r.manager }

// MessageClaims is the runtime's one registry of Connect messages its pulls
// are delivering or have written; every Cloud of this runtime shares it.
func (r *Runtime) MessageClaims() *connect.MessageClaims { return &r.claims }

// AppSchemaCacheFor returns the decorated-schema cache of session. The caller
// holds a Manager lease for it; a retired Session gets a cache of its own that
// cannot replace the current connection's.
func (r *Runtime) AppSchemaCacheFor(session *watchsession.Session) *apps.SchemaCache {
	current, epoch := r.manager.Snapshot()
	if current != session {
		epoch = 0
	}

	return r.schemas.For(session, epoch)
}

// Context is the runtime lifetime used by server requests and owned workers.
func (r *Runtime) Context() context.Context { return r.ctx }

// RequestContext observes both a client's cancellation and runtime shutdown.
// This also covers MCP sessions whose protocol lifetime outlives an HTTP request.
func (r *Runtime) RequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	bound, cancel := context.WithCancel(ctx)

	stop := context.AfterFunc(r.ctx, cancel)
	if r.ctx.Err() != nil {
		cancel()
	}

	return bound, func() { stop(); cancel() }
}

// Stop rejects new work and cancels all existing owners without waiting.
func (r *Runtime) Stop() {
	r.manager.Gate().Stop()
	r.cancel()
}

// Close cancels and joins jobs, then closes the watch. One caller budget covers
// every owner. A deadline is incomplete shutdown, never installer readiness.
func (r *Runtime) Close(ctx context.Context) error {
	r.Stop()
	return errors.Join(r.Sync.Close(ctx), r.Activities.Close(ctx), r.closeWatch(ctx), r.manager.Gate().Wait(ctx))
}

// closeWatch joins the Manager's cleanup and drops the connection's caches.
func (r *Runtime) closeWatch(ctx context.Context) error {
	err := r.manager.Close(ctx)
	r.schemas.Close()

	return err
}
