package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// ErrCapabilityUnavailable identifies an unsupported optional device capability.
var ErrCapabilityUnavailable = watchsession.ErrCapabilityUnavailable

// Info returns immutable connection metadata.
func (s *Service) Info() device.Info { return s.session.Info() }

// List lists a device directory within the caller's current lease.
func (s *Service) List(ctx context.Context, p string) ([]device.Entry, error) {
	return s.session.List(ctx, p)
}

// Stat reads device metadata within the caller's current lease.
func (s *Service) Stat(ctx context.Context, p string) (device.Entry, error) {
	return s.session.Stat(ctx, p)
}

// ReadFile reads fresh bytes without looking up or populating the raw cache.
func (s *Service) ReadFile(ctx context.Context, p string) ([]byte, error) {
	return s.session.ReadFile(ctx, p)
}

// ReadCached reads independently owned bytes through the raw cache.
func (s *Service) ReadCached(ctx context.Context, p string) ([]byte, error) {
	return s.session.ReadCached(ctx, p)
}

// WriteFile invalidates raw caches around the write attempt.
func (s *Service) WriteFile(ctx context.Context, p string, data []byte) error {
	return s.session.WriteFile(ctx, p, data)
}

// DeleteFile invalidates raw caches around the deletion attempt.
func (s *Service) DeleteFile(ctx context.Context, p string) error {
	return s.session.DeleteFile(ctx, p)
}

// Mkdir invalidates raw caches around the directory creation attempt.
func (s *Service) Mkdir(ctx context.Context, p string) error {
	return s.session.Mkdir(ctx, p)
}

// StorageInfo reads the optional transport storage report.
func (s *Service) StorageInfo(ctx context.Context) (*device.Storage, error) {
	return s.session.StorageInfo(ctx)
}

// ListHidden uses the optional hidden filesystem capability.
func (s *Service) ListHidden(ctx context.Context, dir string) ([]device.Entry, error) {
	return s.session.ListHidden(ctx, dir)
}

// WriteHidden invalidates raw caches around a hidden-file write attempt.
func (s *Service) WriteHidden(ctx context.Context, dir, name string, data []byte) error {
	return s.session.WriteHidden(ctx, dir, name, data)
}

// DeleteHidden invalidates raw caches around a hidden-file deletion attempt.
func (s *Service) DeleteHidden(ctx context.Context, dir, name string) error {
	return s.session.DeleteHidden(ctx, dir, name)
}

// RawMTPRead runs a diagnostic data-in operation with cache invalidation.
func (s *Service) RawMTPRead(ctx context.Context, opcode uint32, params []uint32) ([]byte, uint32, []uint32, error) {
	return s.session.RawMTPRead(ctx, opcode, params)
}

// SupportsRawMTP reports capability availability without invoking the device.
func (s *Service) SupportsRawMTP() bool { return s.session.SupportsRawMTP() }
