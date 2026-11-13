package application

import (
	"context"
	"errors"
)

// RollbackRequest explicitly selects the retained package and older data copy.
type RollbackRequest struct {
	Package    PreflightRequest
	SnapshotID string
}

// RollbackView binds both package versions and the data review to one preparation.
type RollbackView struct {
	Installation PreflightInfo
	Data         DataRestoreView
}

// RollbackConsent separately confirms installer handoff and the exact data restore.
type RollbackConsent struct {
	Installation InstallConsent
	Data         RestoreConsent
}

// RollbackResult records irreversible stop, data effects and uncertain installer commit.
type RollbackResult struct {
	Handoff InstallHandoffResult
	Data    *DataRestoreResult
}

// PreparedRollback keeps its packages and maintenance private until Close or Run.
type PreparedRollback struct {
	owner    *RollbackAction
	prepared *PreparedUpdate
	data     *DataRestorePlan
	used     bool
}

// View returns independent review data, never the underlying installation authority.
func (p *PreparedRollback) View() RollbackView {
	return RollbackView{Installation: p.prepared.Info(), Data: p.data.View()}
}

// Close cancels and joins borrowed preparation before returning admission.
func (p *PreparedRollback) Close() error { return p.prepared.Close() }

// RollbackAction reuses the existing package, backup, restore and handoff owners.
type RollbackAction struct {
	preflight *PreflightAction
	backup    *BackupDataAction
	restore   *RestoreDataAction
	handoff   *InstallHandoffAction
}

// NewRollbackAction creates no runtime, installer process or public endpoint.
func NewRollbackAction(preflight *PreflightAction, backup *BackupDataAction, restore *RestoreDataAction, handoff *InstallHandoffAction) *RollbackAction {
	return &RollbackAction{preflight: preflight, backup: backup, restore: restore, handoff: handoff}
}

// Prepare retains an older verified pair and a fresh safety copy before review.
func (a *RollbackAction) Prepare(ctx context.Context, request RollbackRequest) (result *PreparedRollback, err error) {
	if a.preflight == nil || a.backup == nil || a.restore == nil || a.handoff == nil || !validPackageHash(request.SnapshotID) {
		return nil, errors.New("rollback requires configured owned ports and an explicit data snapshot")
	}

	prepared, err := a.preflight.run(ctx, request.Package, true)
	if err != nil {
		return nil, err
	}

	defer func() {
		if result == nil {
			err = errors.Join(err, prepared.Close())
		}
	}()

	if err := validateHandoffTarget(prepared.Info().Inventory); err != nil {
		return nil, err
	}

	if _, err := a.backup.Run(ctx, prepared); err != nil {
		return nil, err
	}

	var data *DataRestorePlan

	err = prepared.withInfo(ctx, func(bound context.Context, _ PreflightInfo) error {
		var err error

		data, err = a.restore.Prepare(bound, prepared.current, prepared.update, request.SnapshotID, prepared.snapshot.ID)

		return err
	})
	if err != nil {
		return nil, err
	}

	return &PreparedRollback{owner: a, prepared: prepared, data: data}, nil
}

// Run restores only after owned stop and commits the installer at most once.
func (a *RollbackAction) Run(ctx context.Context, rollback *PreparedRollback, consent RollbackConsent) (*RollbackResult, error) {
	if rollback == nil || rollback.owner != a {
		return nil, errors.New("rollback preparation belongs to another action")
	}

	var result *RollbackResult

	entered := false

	err := rollback.prepared.withInfo(ctx, func(bound context.Context, info PreflightInfo) error {
		entered = true

		if rollback.used {
			return errors.New("rollback was already attempted; inspect and prepare again")
		}

		rollback.used = true
		if err := a.validate(rollback, info, consent); err != nil {
			return err
		}

		if err := a.handoff.recheck(bound, info.Inventory); err != nil {
			return err
		}

		result = &RollbackResult{Handoff: InstallHandoffResult{From: info.Current.Tag, To: info.Update.Tag, SnapshotID: rollback.prepared.snapshot.ID}}
		input := InstallationInput{Info: info, Snapshot: *rollback.prepared.snapshot, Current: rollback.prepared.current, Update: rollback.prepared.update}

		return a.handoff.transfer(bound, input, &result.Handoff, rollback.prepared, func(stopped context.Context) error {
			var err error

			result.Data, err = a.restore.Run(stopped, rollback.data, consent.Data)
			if err == nil && (result.Data == nil || !result.Data.Completed) {
				return errors.New("rollback data restoration did not complete")
			}

			return err
		})
	})
	if entered || !errors.Is(err, ErrPreparationBusy) {
		err = errors.Join(err, rollback.Close())
	}

	return result, err
}

func (a *RollbackAction) validate(rollback *PreparedRollback, info PreflightInfo, consent RollbackConsent) error {
	if !consent.Data.Confirmed || !consent.Data.EditsResolved || consent.Data.ReviewID != rollback.data.view.ReviewID {
		return errors.New("confirm the exact rollback data review and resolve edits")
	}

	return a.handoff.validate(rollback.prepared, info, consent.Installation)
}
