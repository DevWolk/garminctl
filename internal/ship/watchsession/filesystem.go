package watchsession

import (
	"context"
	"errors"
	"fmt"

	"github.com/DevWolk/garminctl/internal/device"
)

// ErrCapabilityUnavailable identifies an optional transport capability that
// the connected device does not implement.
var ErrCapabilityUnavailable = errors.New("device capability unavailable")

func capabilityError(name string) error { return fmt.Errorf("%w: %s", ErrCapabilityUnavailable, name) }

// Info returns immutable connection metadata without exposing the transport.
func (s *Session) Info() device.Info { return s.dev.Info() }

// List lists a device directory within the caller's current lease.
func (s *Session) List(ctx context.Context, p string) ([]device.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return s.dev.List(ctx, p)
}

// Stat reads device metadata within the caller's current lease.
func (s *Session) Stat(ctx context.Context, p string) (device.Entry, error) {
	if err := ctx.Err(); err != nil {
		return device.Entry{}, err
	}

	return s.dev.Stat(ctx, p)
}

// ReadFile returns fresh bytes without looking up or populating the raw cache.
// It preserves the policy of former direct Device.Read callers.
func (s *Session) ReadFile(ctx context.Context, p string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return s.dev.Read(ctx, p)
}

// ReadCached returns defensively copied bytes from the generation-aware cache.
func (s *Session) ReadCached(ctx context.Context, p string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return s.ReadThrough(ctx, p, true)
}

func (s *Session) mutate(ctx context.Context, run func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.Refresh()
	defer s.Refresh()

	return run()
}

// WriteFile invalidates raw caches before and after a write attempt, including
// failures that may have changed storage or deleted the prior object.
func (s *Session) WriteFile(ctx context.Context, p string, data []byte) error {
	return s.mutate(ctx, func() error { return s.dev.Write(ctx, p, data) })
}

// DeleteFile invalidates raw caches around a deletion attempt.
func (s *Session) DeleteFile(ctx context.Context, p string) error {
	return s.mutate(ctx, func() error { return s.dev.Delete(ctx, p) })
}

// Mkdir invalidates raw caches around a directory creation attempt.
func (s *Session) Mkdir(ctx context.Context, p string) error {
	return s.mutate(ctx, func() error { return s.dev.Mkdir(ctx, p) })
}

// StorageInfo reads the optional transport storage report.
func (s *Session) StorageInfo(ctx context.Context) (*device.Storage, error) {
	reporter, ok := s.dev.(device.StorageReporter)
	if !ok {
		return nil, capabilityError("storage report")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	storage, err := reporter.Storage(ctx)
	if err != nil {
		return nil, err
	}

	return &storage, nil
}

// ListHidden uses the existing optional hidden filesystem capability.
func (s *Session) ListHidden(ctx context.Context, dir string) ([]device.Entry, error) {
	hidden, ok := s.dev.(device.HiddenFS)
	if !ok {
		return nil, capabilityError("hidden filesystem")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return hidden.ListHidden(ctx, dir)
}

// WriteHidden invalidates raw caches around a hidden-file write attempt.
func (s *Session) WriteHidden(ctx context.Context, dir, name string, data []byte) error {
	hidden, ok := s.dev.(device.HiddenFS)
	if !ok {
		return capabilityError("hidden filesystem")
	}

	return s.mutate(ctx, func() error { return hidden.WriteHidden(ctx, dir, name, data) })
}

// DeleteHidden invalidates raw caches around a hidden-file deletion attempt.
func (s *Session) DeleteHidden(ctx context.Context, dir, name string) error {
	hidden, ok := s.dev.(device.HiddenFS)
	if !ok {
		return capabilityError("hidden filesystem")
	}

	return s.mutate(ctx, func() error { return hidden.DeleteHidden(ctx, dir, name) })
}

// RawMTPRead runs one optional data-in operation within the caller's lease.
// Unknown opcodes can mutate storage: invalidate globally and never add retry.
func (s *Session) RawMTPRead(ctx context.Context, opcode uint32, params []uint32) ([]byte, uint32, []uint32, error) {
	raw, ok := s.dev.(device.RawMTPReader)
	if !ok {
		return nil, 0, nil, capabilityError("raw MTP")
	}

	if err := ctx.Err(); err != nil {
		return nil, 0, nil, err
	}

	s.Refresh()
	defer s.Refresh()

	return raw.MtpRead(ctx, opcode, params)
}

// SupportsRawMTP reports availability without exposing or invoking the device.
func (s *Session) SupportsRawMTP() bool {
	_, ok := s.dev.(device.RawMTPReader)
	return ok
}
