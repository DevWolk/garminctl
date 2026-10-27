// Package lifecycle coordinates process-local work admission and maintenance.
// It owns no devices, job results, cancellation callbacks or platform resources.
package lifecycle

import (
	"context"
	"errors"
	"sync"
)

// Admission errors distinguish temporary maintenance, active work and shutdown.
var (
	ErrMaintenance = errors.New("application maintenance is in progress")
	ErrStopped     = errors.New("application is stopping")
	ErrBusy        = errors.New("application has active operations or jobs")
	ErrNotStopped  = errors.New("work admission must stop before waiting for shutdown")
)

// State describes admission, not the lifetime of external resources. Closed
// means permits drained; the owner still closes its devices and server workers.
type State uint8

// Admission states transition from Running through maintenance or shutdown.
const (
	Running State = iota
	Maintenance
	Stopping
	Closed
)

// Snapshot is an immutable copy of admission state. Operations include callers
// waiting for a device lease; Jobs include registered work between device uses.
type Snapshot struct {
	State      State
	Operations int
	Jobs       int
}

// Gate atomically admits operations/jobs or an exclusive maintenance owner.
// Its zero value accepts work. The runtime owns cancellation and calls Stop
// before cancelling/joining workers; Gate never invokes callbacks under its lock.
type Gate struct {
	mu         sync.Mutex
	state      State
	operations int
	jobs       int
	idle       chan struct{}
}

// Enter reserves an operation before it waits for a device. The returned release
// must be called on every path, including rejected lease acquisition; it is safe
// to call concurrently and more than once.
func (g *Gate) Enter(ctx context.Context) (release func(), err error) {
	return g.enter(ctx, false)
}

// EnterJob reserves background work, including time spent outside device leases.
func (g *Gate) EnterJob(ctx context.Context) (release func(), err error) {
	return g.enter(ctx, true)
}

func (g *Gate) enter(ctx context.Context, job bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := g.admissionError(); err != nil {
		return nil, err
	}

	if g.operations+g.jobs == 0 {
		g.idle = make(chan struct{})
	}

	if job {
		g.jobs++
	} else {
		g.operations++
	}

	var once sync.Once

	return func() { once.Do(func() { g.release(job) }) }, nil
}

// admissionError is called with mu held.
func (g *Gate) admissionError() error {
	switch g.state {
	case Maintenance:
		return ErrMaintenance
	case Stopping, Closed:
		return ErrStopped
	default:
		return nil
	}
}

func (g *Gate) release(job bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if job {
		g.jobs--
	} else {
		g.operations--
	}

	if g.operations+g.jobs == 0 {
		close(g.idle)

		g.idle = nil
		if g.state == Stopping {
			g.state = Closed
		}
	}
}

// TryMaintenance enters only when no operation or job is reserved. Busy leaves
// ordinary admission running. The returned resume is idempotent; it cannot
// reopen a Gate that has subsequently been stopped.
func (g *Gate) TryMaintenance(ctx context.Context) (resume func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := g.admissionError(); err != nil {
		return nil, err
	}

	if g.operations+g.jobs != 0 {
		return nil, ErrBusy
	}

	g.state = Maintenance

	var once sync.Once

	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()

			if g.state == Maintenance {
				g.state = Running
			}
		})
	}, nil
}

// Stop irreversibly rejects new work. It does not cancel existing work; the
// runtime cancels its owned contexts outside this lock, then calls Wait.
func (g *Gate) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.state = Stopping
	if g.operations+g.jobs == 0 {
		g.state = Closed
	}
}

// Wait joins existing permits after Stop. The caller passes the same shutdown
// deadline it uses for other owners. Expiry does not pretend that work finished.
func (g *Gate) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	g.mu.Lock()
	if g.state != Stopping && g.state != Closed {
		g.mu.Unlock()
		return ErrNotStopped
	}

	idle := g.idle
	g.mu.Unlock()

	if idle == nil {
		return nil
	}

	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Snapshot returns state without waiting for operations, jobs or transport I/O.
func (g *Gate) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()

	return Snapshot{State: g.state, Operations: g.operations, Jobs: g.jobs}
}
