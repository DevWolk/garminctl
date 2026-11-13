// Package native supplies application reads and package preparation to a native host.
package native

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

const phaseCanceling = "canceling"

// Scope borrows the host's existing admission, cancellation and join boundary.
type Scope interface {
	Acquire() (context.Context, func(), error)
	Backend() (string, bool, error)
}

// Reader is assembled by app with compiled version and trusted metadata sources.
type Reader interface {
	Info(context.Context) (*application.InventoryResult, error)
	History(context.Context, bool, bool) (*application.ReleaseHistoryResult, error)
}

// Factory constructs a reader without IO from local backend observations.
type Factory func(string, bool) (Reader, error)

// Service exposes application metadata and window-owned package preparation.
type Service struct {
	scope        Scope
	factory      Factory
	mu           sync.Mutex
	reader       Reader
	operationMu  sync.Mutex
	operation    *downloadOperation
	nextDownload uint64
	installation *installationOperation
	nextInstall  uint64
}

// NewService constructs no runtime, requests or background work.
func NewService(scope Scope, factory Factory) *Service {
	return &Service{scope: scope, factory: factory}
}

// Info reads independent desktop, CLI and backend observations.
func (s *Service) Info() (*application.InventoryResult, error) {
	return invokeRead(s, func(ctx context.Context, r Reader) (*application.InventoryResult, error) { return r.Info(ctx) })
}

// History reads saved releases with current channel policy, without network IO.
func (s *Service) History(includePrerelease bool) (*application.ReleaseHistoryResult, error) {
	return invokeRead(s, func(ctx context.Context, r Reader) (*application.ReleaseHistoryResult, error) {
		return r.History(ctx, includePrerelease, false)
	})
}

// Check explicitly refreshes releases, with visible cached fallback on failure.
func (s *Service) Check(includePrerelease bool) (*application.ReleaseHistoryResult, error) {
	return invokeRead(s, func(ctx context.Context, r Reader) (*application.ReleaseHistoryResult, error) {
		return r.History(ctx, includePrerelease, true)
	})
}

func invokeRead[T any](s *Service, read func(context.Context, Reader) (*T, error)) (*T, error) {
	if s.scope == nil {
		return nil, errors.New("native window is not configured")
	}

	ctx, release, err := s.scope.Acquire()
	if err != nil {
		return nil, err
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	reader, err := s.source()
	if err != nil {
		return nil, err
	}

	result, err := read(ctx, reader)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}

	return result, err
}

func (s *Service) source() (Reader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.reader != nil {
		return s.reader, nil
	}

	if s.factory == nil {
		return nil, errors.New("application information is not configured")
	}

	address, owned, err := s.scope.Backend()
	if err != nil {
		return nil, err
	}

	reader, err := s.factory(address, owned)
	if err != nil {
		return nil, err
	}

	if reader == nil {
		return nil, errors.New("application reader is unavailable")
	}

	s.reader = reader

	return reader, nil
}
