package garmin

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Runtime owns the shared watch and jobs of one process backend. This temporary
// current-package composition moves to app as the feature packages are extracted.
type Runtime struct {
	Watch      *Lazy
	Sync       *SyncCoordinator
	Activities *ActivityCoordinator
	ctx        context.Context //nolint:containedctx // The runtime owns, cancels and joins this lifetime, never a request lifetime.
	cancel     context.CancelFunc
}

// NewRuntime constructs one admission gate for all watch operations and jobs.
func NewRuntime(parent context.Context, open Opener) *Runtime {
	ctx, cancel := context.WithCancel(parent)
	watch := NewLazyWithGate(open, new(lifecycle.Gate))

	return &Runtime{Watch: watch, Sync: NewSyncCoordinator(ctx, watch.Gate()), Activities: NewActivityCoordinator(ctx, watch), ctx: ctx, cancel: cancel}
}

// Manager is the runtime's watch owner.
func (r *Runtime) Manager() *watchsession.Manager { return r.Watch.Manager() }

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
	r.Watch.Gate().Stop()
	r.cancel()
}

// Close cancels and joins jobs, then closes the watch. One caller budget covers
// every owner. A deadline is incomplete shutdown, never installer readiness.
func (r *Runtime) Close(ctx context.Context) error {
	r.Stop()
	return errors.Join(r.Sync.Close(ctx), r.Activities.Close(ctx), r.Watch.Close(ctx), r.Watch.Gate().Wait(ctx))
}
