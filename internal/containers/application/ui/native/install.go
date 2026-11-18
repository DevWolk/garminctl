package native

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

// Installer borrows the existing preflight, snapshot and owned handoff policy.
type Installer interface {
	Install(context.Context, application.PreflightRequest, application.InstallConsent, func(string)) (*application.InstallHandoffResult, error)
}

// UpdateHost exits only after the service has released its window operation.
type UpdateHost interface{ ExitForUpdate() }

// InstallationRecords reads what the installer helper recorded after the
// application last closed for an update.
type InstallationRecords interface {
	LastInstallation(context.Context) (*application.InstallationOutcome, error)
}

// InstallationState describes one attempt; acceptance never proves OS installation.
type InstallationState struct {
	Kind          string                            `json:"kind,omitempty"`
	Review        *application.DataRestoreView      `json:"review,omitempty"`
	Restore       *application.DataRestoreResult    `json:"restore,omitempty"`
	ID            uint64                            `json:"id"`
	Tag           string                            `json:"tag,omitempty"`
	Phase         string                            `json:"phase"`
	Active        bool                              `json:"active"`
	CanCancel     bool                              `json:"canCancel"`
	StopRequested bool                              `json:"stopRequested"`
	Result        *application.InstallHandoffResult `json:"result,omitempty"`
	Error         string                            `json:"error,omitempty"`
	// LastResult is set while idle when the previous installation did not complete.
	LastResult string `json:"lastResult,omitempty"`
}

type installationOperation struct {
	state        InstallationState
	cancel       context.CancelFunc
	confirmation chan application.RollbackConsent
}

// StartInstallation requires explicit consent and accepts only a retained cache ID.
func (s *Service) StartInstallation(request application.PreflightRequest, consent application.InstallConsent) (*InstallationState, error) {
	if !consent.Confirmed || !consent.EditsResolved {
		return nil, errors.New("confirm installation and resolve unsaved edits first")
	}

	ctx, release, installer, host, err := s.installationSource()
	if err != nil {
		return nil, err
	}

	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if err := s.preparationErrorLocked(); err != nil {
		release()
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	s.nextInstall++
	op := &installationOperation{state: InstallationState{ID: s.nextInstall, Tag: request.Tag, Phase: "verifying", Active: true, CanCancel: true}, cancel: cancel}
	s.installation = op

	go s.runInstallation(ctx, sync.OnceFunc(release), installer, host, op, request, consent)

	return cloneInstallationState(op.state), nil
}

func (s *Service) installationSource() (context.Context, func(), Installer, UpdateHost, error) {
	ctx, release, reader, host, err := s.ownedInstallationSource()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	installer, ok := reader.(Installer)
	if !ok {
		release()
		return nil, nil, nil, nil, errors.New("application installation is unavailable")
	}

	return ctx, release, installer, host, nil
}

func (s *Service) ownedInstallationSource() (context.Context, func(), Reader, UpdateHost, error) {
	if s.scope == nil {
		return nil, nil, nil, nil, errors.New("native window is not configured")
	}

	host, ok := s.scope.(UpdateHost)
	if !ok {
		return nil, nil, nil, nil, errors.New("native update exit is unavailable")
	}

	ctx, release, err := s.scope.Acquire()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	_, owned, err := s.scope.Backend()
	if err != nil || !owned {
		release()
		return nil, nil, nil, nil, errors.Join(err, errors.New("stop the separately owned backend before updating"))
	}

	reader, err := s.source()
	if err != nil {
		release()
		return nil, nil, nil, nil, err
	}

	return ctx, release, reader, host, nil
}

func (s *Service) runInstallation(ctx context.Context, release func(), installer Installer, host UpdateHost, op *installationOperation, request application.PreflightRequest, consent application.InstallConsent) {
	defer release()
	defer op.cancel()

	result, err := installer.Install(ctx, request, consent, func(phase string) { s.updateInstallation(op, phase) })

	s.finishAndExit(ctx, release, host, op, result, err)
}

func (s *Service) finishAndExit(ctx context.Context, release func(), host UpdateHost, op *installationOperation, result *application.InstallHandoffResult, err error) {
	if s.finishInstallation(ctx, op, result, err) {
		if err != nil {
			slog.Error("application installer handoff has an uncertain outcome; automatic retry is disabled", "error", err)
		}

		release()
		host.ExitForUpdate()
	}
}

// InstallationStatus resumes observation after page reload without starting work.
func (s *Service) InstallationStatus() (*InstallationState, error) {
	return invokeRead(s, func(ctx context.Context, reader Reader) (*InstallationState, error) {
		s.operationMu.Lock()
		defer s.operationMu.Unlock()

		if s.installation == nil {
			return &InstallationState{Phase: "idle", LastResult: lastIncompleteInstallation(ctx, reader)}, nil
		}

		return cloneInstallationState(s.installation.state), nil
	})
}

// lastIncompleteInstallation is the only place a failure after handoff reaches
// the user: the application had already closed when the helper recorded it.
func lastIncompleteInstallation(ctx context.Context, reader Reader) string {
	records, ok := reader.(InstallationRecords)
	if !ok {
		return ""
	}

	outcome, err := records.LastInstallation(ctx)
	if err != nil {
		slog.Warn("the last installation result cannot be read", "error", err)
		return ""
	}

	if outcome == nil || !outcome.Incomplete() {
		return ""
	}

	// The record outlives a later manual installation of the same or a newer version.
	if info, err := reader.Info(ctx); err == nil && info != nil && outcome.Superseded(info.Desktop.Version) {
		return ""
	}

	return outcome.Summary()
}

// CancelInstallation cancels only preparation and retains admission until cleanup joins.
func (s *Service) CancelInstallation(id uint64) (*InstallationState, error) {
	return invokeRead(s, func(context.Context, Reader) (*InstallationState, error) {
		s.operationMu.Lock()
		defer s.operationMu.Unlock()

		if s.installation == nil || s.installation.state.ID != id {
			return nil, errors.New("installation is no longer current")
		}

		op := s.installation
		if op.state.Active && !op.state.CanCancel {
			return nil, errors.New("installer handoff has started; do not retry or interrupt installation")
		}

		if op.state.Active {
			op.state.Phase = phaseCanceling
			op.state.CanCancel = false
			op.cancel()
		}

		return cloneInstallationState(op.state), nil
	})
}

func (s *Service) updateInstallation(op *installationOperation, phase string) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if s.installation == op && op.state.Active && op.state.Phase != phaseCanceling {
		op.state.Phase = phase
		op.state.CanCancel = phase != "handoff"
	}
}

func (s *Service) finishInstallation(ctx context.Context, op *installationOperation, result *application.InstallHandoffResult, err error) bool {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	op.state.Active, op.state.CanCancel = false, false

	if result != nil {
		value := *result
		op.state.Result = &value
		op.state.StopRequested = result.State >= application.HandoffStopRequested
	}

	if result != nil && result.State >= application.HandoffCommitAttempted {
		op.state.Phase = "accepted"
		if err != nil || result.State != application.HandoffAccepted {
			op.state.Phase = "uncertain"
		}

		if err != nil {
			op.state.Error = err.Error()
		}

		return true
	}

	op.state.Phase = "failed"

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		op.state.Phase = "canceled"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// Every layer below reports the same expired deadline; say it once.
		op.state.Error = "The operation ran out of time before the installer was started; nothing was installed."
	case err != nil:
		op.state.Error = err.Error()
	default:
		op.state.Error = "Installation returned no accepted handoff."
	}

	return false
}

func cloneInstallationState(state InstallationState) *InstallationState {
	if state.Review != nil {
		state.Review = cloneRollbackReview(*state.Review)
	}

	if state.Restore != nil {
		value := *state.Restore
		state.Restore = &value
	}

	if state.Result != nil {
		value := *state.Result
		state.Result = &value
	}

	return &state
}

func (s *Service) preparationErrorLocked() error {
	if s.operation != nil && s.operation.state.Active {
		return errors.New("application preparation is already running")
	}

	if s.installation == nil {
		return nil
	}

	state := s.installation.state

	if state.StopRequested {
		return errors.New("application shutdown was requested; restart the app before another preparation")
	}

	if state.Active {
		return errors.New("application preparation is already running")
	}

	return nil
}

func cloneRollbackReview(value application.DataRestoreView) *application.DataRestoreView {
	value.Changes = slices.Clone(value.Changes)
	return &value
}
