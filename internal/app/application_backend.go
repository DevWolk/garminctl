package app

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// ApplicationBackend lends an owned backend's maintenance admission to Application.
// Its zero value creates no runtime and performs no IO.
type ApplicationBackend struct {
	mu, dataRestoreMu sync.Mutex
	runtime           *process.Runtime
	address           string
	started           bool
	done              chan struct{}
	runErr            error
}

// ServeListener starts the owned backend after the native host has reserved its listener.
func (b *ApplicationBackend) ServeListener(ctx context.Context, listener net.Listener, open watchsession.Opener, source string, autoSync bool, version string) (result error) {
	if b == nil {
		return errors.Join(errors.New("owned backend control is not configured"), listener.Close())
	}

	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return errors.Join(errors.New("owned backend control was already used"), listener.Close())
	}

	b.started = true
	b.done = make(chan struct{})
	b.mu.Unlock()

	runtime := NewRuntime(ctx, open)

	b.mu.Lock()
	b.runtime, b.address = runtime, "http://"+listener.Addr().String()
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.runtime = nil
		b.runErr = result
		close(b.done)
		b.mu.Unlock()
	}()

	return serveListenerRuntime(listener, runtime, source, autoSync, version)
}

func (b *ApplicationBackend) maintenanceFor(address string, owned bool) func(context.Context) (func(), error) {
	return func(ctx context.Context) (func(), error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if !owned {
			return nil, errors.New("the backend belongs to another process; stop it before updating")
		}

		runtime, err := b.current(address)
		if err != nil {
			return nil, err
		}

		return reserveApplicationMaintenance(ctx, runtime)
	}
}

func (b *ApplicationBackend) current(address string) (*process.Runtime, error) {
	if b == nil {
		return nil, errors.New("owned backend control is not configured")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.runtime == nil {
		if b.started {
			return nil, lifecycle.ErrStopped
		}

		return nil, errors.New("owned backend is not ready")
	}

	if address != b.address {
		return nil, errors.New("maintenance does not belong to this backend address")
	}

	if b.runtime.Context().Err() != nil {
		return nil, lifecycle.ErrStopped
	}

	return b.runtime, nil
}

func reserveApplicationMaintenance(ctx context.Context, runtime *process.Runtime) (func(), error) {
	bound, cancel := runtime.RequestContext(ctx)

	resume, err := runtime.Manager().Gate().TryMaintenance(bound)
	if err != nil {
		cancel()
		return nil, err
	}

	finish := sync.OnceFunc(func() { resume(); cancel() })
	stop := context.AfterFunc(bound, finish)

	release := sync.OnceFunc(func() { stop(); finish() })
	if err := bound.Err(); err != nil {
		release()
		return nil, err
	}

	return release, nil
}
