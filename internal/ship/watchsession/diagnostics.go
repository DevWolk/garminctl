package watchsession

import (
	"context"
	"errors"
	"sync"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

// Diagnostics is a borrowed handle-operation scope, created only for a
// supported transport. It never exposes the raw device. Operations after its
// callback returns fail; admitted concurrent operations are cancelled and joined.
type Diagnostics struct {
	owner  *Session
	raw    device.MTPDiagnostics
	ctx    context.Context //nolint:containedctx // bounded caller lease; cancelled and joined when the diagnostic callback ends
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	active sync.WaitGroup
	slot   chan struct{}
	fatal  error
}

// WithDiagnostics lends handle operations for this callback and current lease.
// Mutations invalidate raw caches even if transport verification fails.
func (s *Session) WithDiagnostics(ctx context.Context, run func(*Diagnostics) error) (err error) {
	raw, ok := s.dev.(device.MTPDiagnostics)
	if !ok {
		return capabilityError("MTP handle diagnostics")
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if run == nil {
		return errors.New("diagnostic callback is required")
	}

	scopeCtx, cancel := context.WithCancel(ctx)

	d := &Diagnostics{owner: s, raw: raw, ctx: scopeCtx, cancel: cancel, slot: make(chan struct{}, 1)}
	defer func() {
		if fatal := d.close(); fatal != nil && !errors.Is(err, device.ErrDisconnected) {
			err = errors.Join(err, fatal)
		}
	}()

	return run(d)
}

func (d *Diagnostics) begin() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.errorLocked(); err != nil {
		return err
	}

	d.active.Add(1)

	return nil
}

func (d *Diagnostics) recordFailure(err error) {
	if errors.Is(err, device.ErrDisconnected) {
		d.mu.Lock()
		if d.fatal == nil {
			d.fatal = err
		}

		d.closed = true
		d.mu.Unlock()
		d.cancel()
	}
}

func (d *Diagnostics) errorLocked() error {
	if d.fatal != nil {
		return d.fatal
	}

	if d.closed {
		return context.Canceled
	}

	return d.ctx.Err()
}

func (d *Diagnostics) scopeError() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.errorLocked()
}

func (d *Diagnostics) close() error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	d.cancel()
	d.active.Wait()

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.fatal
}

func diagnosticCall[T any](d *Diagnostics, mutating bool, run func(context.Context) (T, error)) (value T, err error) {
	if err := d.begin(); err != nil {
		return value, err
	}

	defer d.active.Done()

	// A WPD SendFile spans two protocol calls. Serialize complete diagnostic
	// operations so callers within the same borrowed scope cannot interleave them.
	select {
	case d.slot <- struct{}{}:
		defer func() { <-d.slot }()
	case <-d.ctx.Done():
		return value, d.scopeError()
	}

	if err := d.scopeError(); err != nil {
		return value, err
	}

	// Record fatal transport state before releasing the operation slot, so
	// queued calls cannot send another command to the broken connection.
	defer func() { d.recordFailure(err) }()

	if mutating {
		d.owner.Refresh()
		defer d.owner.Refresh()
	}

	return run(d.ctx)
}

type diagnosticHandles struct{ storage, handle uint32 }

// Handles resolves a visible path to its storage/object handles.
func (d *Diagnostics) Handles(path string) (uint32, uint32, error) {
	pair, err := diagnosticCall(d, false, func(ctx context.Context) (diagnosticHandles, error) {
		storage, handle, err := d.raw.Handles(ctx, path)
		return diagnosticHandles{storage, handle}, err
	})

	return pair.storage, pair.handle, err
}

// AllChildren lists objects including hidden children.
func (d *Diagnostics) AllChildren(storage, parent uint32) ([]device.MTPObject, error) {
	return diagnosticCall(d, false, func(ctx context.Context) ([]device.MTPObject, error) {
		return d.raw.AllChildren(ctx, storage, parent)
	})
}

type diagnosticChild struct {
	object device.MTPObject
	found  bool
}

// FindChild locates a named child within the borrowed scope.
func (d *Diagnostics) FindChild(storage, parent uint32, name string) (device.MTPObject, bool, error) {
	child, err := diagnosticCall(d, false, func(ctx context.Context) (diagnosticChild, error) {
		object, found, err := d.raw.FindChild(ctx, storage, parent, name)
		return diagnosticChild{object, found}, err
	})

	return child.object, child.found, err
}

// SendFile writes a handle-addressed file and invalidates the raw cache.
func (d *Diagnostics) SendFile(storage, parent uint32, name string, data []byte) (uint32, error) {
	return diagnosticCall(d, true, func(ctx context.Context) (uint32, error) {
		return d.raw.SendFile(ctx, storage, parent, name, data)
	})
}

// ReadHandle reads one handle without caching its bytes.
func (d *Diagnostics) ReadHandle(handle uint32) ([]byte, error) {
	return diagnosticCall(d, false, func(ctx context.Context) ([]byte, error) { return d.raw.ReadHandle(ctx, handle) })
}

// DeleteHandle deletes a handle-addressed object and invalidates the raw cache.
func (d *Diagnostics) DeleteHandle(handle uint32) error {
	_, err := diagnosticCall(d, true, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, d.raw.DeleteHandle(ctx, handle)
	})

	return err
}
