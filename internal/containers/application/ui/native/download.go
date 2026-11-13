package native

import (
	"context"
	"errors"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

// Downloader borrows preparation policy and cannot install a package.
type Downloader interface {
	DownloadDisabledReason() string
	Download(context.Context, application.DownloadRequest, func(application.DownloadProgress)) (*application.DownloadResult, error)
}

// DownloadState is a snapshot, not permission or paths for an installer.
type DownloadState struct {
	ID  uint64 `json:"id"`
	Tag string `json:"tag,omitempty"`
	application.DownloadProgress
	Active         bool                        `json:"active"`
	Result         *application.DownloadResult `json:"result,omitempty"`
	Error          string                      `json:"error,omitempty"`
	DisabledReason string                      `json:"disabledReason,omitempty"`
}

type downloadOperation struct {
	state  DownloadState
	cancel context.CancelFunc
}

// StartDownload starts one window-owned preparation and returns its identity.
func (s *Service) StartDownload(tag string, includePrerelease bool) (*DownloadState, error) {
	if s.scope == nil {
		return nil, errors.New("native window is not configured")
	}

	if len(tag) == 0 || len(tag) > 256 {
		return nil, errors.New("select a valid application release")
	}

	ctx, release, err := s.scope.Acquire()
	if err != nil {
		return nil, err
	}

	reader, err := s.source()
	if err != nil {
		release()
		return nil, err
	}

	downloader, ok := reader.(Downloader)
	if !ok {
		release()
		return nil, errors.New("application downloads are unavailable")
	}

	if reason := downloader.DownloadDisabledReason(); reason != "" {
		release()
		return nil, errors.New(reason)
	}

	s.operationMu.Lock()
	if err := s.preparationErrorLocked(); err != nil {
		s.operationMu.Unlock()
		release()

		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	s.nextDownload++
	operation := &downloadOperation{state: DownloadState{ID: s.nextDownload, Tag: tag, DownloadProgress: application.DownloadProgress{Phase: "checking"}, Active: true}, cancel: cancel}
	s.operation = operation
	initial := cloneDownloadState(operation.state)
	s.operationMu.Unlock()

	go func() {
		defer release()
		defer cancel()

		result, err := downloader.Download(ctx, application.DownloadRequest{Tag: tag, IncludePrerelease: includePrerelease}, func(p application.DownloadProgress) { s.updateDownload(operation, p) })
		s.finishDownload(ctx, operation, result, err)
	}()

	return initial, nil
}

// DownloadStatus survives page reload while the window owns the operation.
func (s *Service) DownloadStatus() (*DownloadState, error) {
	return invokeRead(s, func(_ context.Context, r Reader) (*DownloadState, error) {
		s.operationMu.Lock()
		defer s.operationMu.Unlock()

		state := DownloadState{Phase: "idle"}
		if s.operation != nil {
			state = s.operation.state
		}

		if d, ok := r.(Downloader); ok {
			state.DisabledReason = d.DownloadDisabledReason()
		} else {
			state.DisabledReason = "Application downloads are unavailable in this build."
		}

		return cloneDownloadState(state), nil
	})
}

// CancelDownload affects only the supplied current operation, never a successor.
func (s *Service) CancelDownload(id uint64) (*DownloadState, error) {
	return invokeRead(s, func(context.Context, Reader) (*DownloadState, error) {
		s.operationMu.Lock()
		defer s.operationMu.Unlock()

		if s.operation == nil || s.operation.state.ID != id {
			return nil, errors.New("download is no longer current")
		}

		if s.operation.state.Active {
			s.operation.state.Phase = phaseCanceling
			s.operation.cancel()
		}

		return cloneDownloadState(s.operation.state), nil
	})
}

func (s *Service) updateDownload(op *downloadOperation, p application.DownloadProgress) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if s.operation == op && op.state.Active && op.state.Phase != phaseCanceling {
		op.state.DownloadProgress = p
	}
}

func (s *Service) finishDownload(ctx context.Context, op *downloadOperation, result *application.DownloadResult, err error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	op.state.Active = false
	if errors.Is(ctx.Err(), context.Canceled) {
		op.state.Phase = "canceled"
		return
	}

	if ctx.Err() != nil {
		err = ctx.Err()
	}

	if err != nil {
		op.state.Phase = "failed"
		op.state.Error = err.Error()

		return
	}

	if result == nil {
		op.state.Phase = "failed"
		op.state.Error = "Download returned no verified package."

		return
	}

	receipt := *result
	op.state.Result = &receipt
	op.state.Phase = "ready"
}

func cloneDownloadState(state DownloadState) *DownloadState {
	if state.Result != nil {
		result := *state.Result
		state.Result = &result
	}

	return &state
}
