package native

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

// RollbackPreparation owns verified packages, data review and maintenance.
type RollbackPreparation interface {
	View() application.RollbackView
	Run(context.Context, application.RollbackConsent) (*application.RollbackResult, error)
	Close() error
}

// RollbackPreparer borrows the same owned backend as ordinary installation.
type RollbackPreparer interface {
	RetainedRollbacks(context.Context, bool) ([]application.RollbackOption, error)
	PrepareRollback(context.Context, application.RollbackRequest) (RollbackPreparation, error)
}

// CloseHolder postpones native window closure while confirmed work must finish.
// A postponed close may run from release, so release follows the Scope lease.
type CloseHolder interface {
	HoldClose() (release func(), err error)
}

// rollbackTimeout bounds preparation with its review, and separately the
// confirmed work.
const rollbackTimeout = 25 * time.Minute

// SavedRollback is unverified local metadata; preparation verifies both copies.
type SavedRollback struct {
	Tag        string    `json:"tag"`
	CacheID    string    `json:"cacheId"`
	SnapshotID string    `json:"snapshotId"`
	CreatedAt  time.Time `json:"createdAt"`
	Files      int       `json:"files"`
	Bytes      int64     `json:"bytes"`
	Prerelease bool      `json:"prerelease"`
}

// SavedRollbacks reads local display metadata only.
func (s *Service) SavedRollbacks(prerelease bool) ([]SavedRollback, error) {
	result, err := invokeRead(s, func(ctx context.Context, r Reader) (*[]SavedRollback, error) {
		source, ok := r.(RollbackPreparer)
		if !ok {
			return nil, errors.New("saved rollback packages are unavailable")
		}

		rows, err := source.RetainedRollbacks(ctx, prerelease)
		if err != nil {
			return nil, err
		}

		values := make([]SavedRollback, 0, len(rows))
		for _, row := range rows {
			values = append(values, SavedRollback{Tag: row.Package.Tag, CacheID: row.Package.ID, SnapshotID: row.SnapshotID, CreatedAt: row.CreatedAt, Files: row.Files, Bytes: row.Bytes, Prerelease: row.Package.Prerelease})
		}

		return &values, nil
	})
	if err != nil {
		return nil, err
	}

	return *result, nil
}

// StartRollback prepares a review; it never restores or installs without confirmation.
func (s *Service) StartRollback(request application.RollbackRequest) (*InstallationState, error) {
	ctx, release, reader, host, err := s.ownedInstallationSource()
	if err != nil {
		return nil, err
	}

	source, ok := reader.(RollbackPreparer)
	if _, holds := s.scope.(CloseHolder); !ok || !holds {
		release()
		return nil, errors.New("application rollback is unavailable")
	}

	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if err := s.preparationErrorLocked(); err != nil {
		release()
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, rollbackTimeout)
	s.nextInstall++
	op := &installationOperation{state: InstallationState{ID: s.nextInstall, Tag: request.Package.Tag, Kind: "rollback", Phase: "verifying", Active: true, CanCancel: true}, cancel: cancel, confirmation: make(chan application.RollbackConsent, 1)}

	s.installation = op
	go s.runRollback(ctx, sync.OnceFunc(release), source, host, op, request)

	return cloneInstallationState(op.state), nil
}

// ConfirmRollback accepts one exact review for the current operation.
func (s *Service) ConfirmRollback(id uint64, consent application.RollbackConsent) (*InstallationState, error) {
	if s.scope == nil {
		return nil, errors.New("native window is not configured")
	}

	ctx, release, err := s.scope.Acquire()
	if err != nil {
		return nil, err
	}

	release = sync.OnceFunc(release)
	defer release()

	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	op := s.installation

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if op == nil || op.state.ID != id || op.state.Kind != "rollback" || op.state.Phase != "review" || !op.state.Active || op.state.Review == nil {
		return nil, errors.New("rollback review is no longer current")
	}

	if !consent.Installation.Confirmed || !consent.Installation.EditsResolved || !consent.Data.Confirmed || !consent.Data.EditsResolved || consent.Data.ReviewID != op.state.Review.ReviewID {
		return nil, errors.New("confirm the displayed rollback and data replacement; resolve unsaved edits first")
	}

	op.state.Phase, op.state.CanCancel = "handoff", false
	result := cloneInstallationState(op.state)
	// The operation already owns Scope. Release this RPC before waking a fast
	// handoff so ExitForUpdate cannot wait on the confirming call's own lease.
	release()

	op.confirmation <- consent

	return result, nil
}

func (s *Service) runRollback(ctx context.Context, release func(), source RollbackPreparer, host UpdateHost, op *installationOperation, request application.RollbackRequest) {
	var unhold func()

	// A postponed window close may quit and join this operation, so the hold
	// ends only after the Scope lease has been released.
	defer func() {
		if unhold != nil {
			unhold()
		}
	}()
	defer release()
	defer op.cancel()

	// Preparation and review end with the window, the Cancel button or their
	// deadline. The preparation context itself stays detached so that confirmed
	// work can outlive all three.
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	detach := context.AfterFunc(ctx, cancel)
	defer detach()

	prepared, consent, err := s.reviewRollback(ctx, work, source, op, request)
	if err == nil {
		unhold, err = s.detachConfirmed(ctx, detach)
	}

	var result *application.RollbackResult

	if err == nil {
		var stop context.CancelFunc

		// Stop, restore and commit get their full deadline from the confirmation.
		ctx, stop = context.WithTimeout(work, rollbackTimeout)
		defer stop()

		result, err = prepared.Run(ctx, consent)
	}

	if prepared != nil {
		err = errors.Join(err, prepared.Close())
	}

	var handoff *application.InstallHandoffResult
	if result != nil {
		handoff = &result.Handoff

		s.operationMu.Lock()
		if result.Data != nil {
			value := *result.Data
			op.state.Restore = &value
		}
		s.operationMu.Unlock()
	}

	s.finishAndExit(ctx, release, host, op, handoff, err)
}

// detachConfirmed keeps the window open and separates confirmed work from the
// review lifetime. It fails when either ended first; nothing has changed then.
func (s *Service) detachConfirmed(ctx context.Context, detach func() bool) (func(), error) {
	holder, ok := s.scope.(CloseHolder)
	if !ok {
		return nil, errors.New("application rollback is unavailable")
	}

	unhold, err := holder.HoldClose()
	if err != nil {
		return nil, err
	}

	detached := detach()

	if err := ctx.Err(); err != nil {
		return unhold, err
	}

	if !detached {
		return unhold, context.Canceled
	}

	return unhold, nil
}

func (s *Service) reviewRollback(ctx, work context.Context, source RollbackPreparer, op *installationOperation, request application.RollbackRequest) (RollbackPreparation, application.RollbackConsent, error) {
	var none application.RollbackConsent

	prepared, err := source.PrepareRollback(work, request)
	if err != nil {
		return prepared, none, err
	}

	if prepared == nil {
		return nil, none, errors.New("rollback returned no owned preparation")
	}

	review := prepared.View().Data
	if review.ReviewID == "" {
		return prepared, none, errors.New("rollback returned no exact data review")
	}

	if err := ctx.Err(); err != nil {
		return prepared, none, err
	}

	s.operationMu.Lock()
	if op.state.Phase != phaseCanceling {
		op.state.Review = cloneRollbackReview(review)
		op.state.Phase = "review"
	}
	s.operationMu.Unlock()

	select {
	case <-ctx.Done():
		return prepared, none, ctx.Err()
	case consent := <-op.confirmation:
		return prepared, consent, nil
	}
}
