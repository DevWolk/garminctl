package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// DataCompatibility checks current selected bytes under caller-owned admission.
// stopped requires that same backend to have joined; it never restores files.
type DataCompatibility interface {
	Verify(context.Context, RecoverySnapshot, RecoverySnapshot, bool) error
}

// NewCompatibleRollbackAction adds current-data checking to the existing owner.
func NewCompatibleRollbackAction(preflight *PreflightAction, backup *BackupDataAction, restore *RestoreDataAction, handoff *InstallHandoffAction, data DataCompatibility) *RollbackAction {
	action := NewRollbackAction(preflight, backup, restore, handoff)
	action.compatibility = data

	return action
}

func (a *RollbackAction) verifyCompatibility(ctx context.Context, p *PreparedRollback, stopped bool) error {
	if a.compatibility == nil || !KeepsCurrentData(p.data.backup, p.data.safety) {
		return errors.New("current data compatibility is unknown; explicitly restore a selected backup instead")
	}

	if err := a.restore.recheck(ctx, p.data); err != nil {
		return err
	}

	return errors.Join(a.compatibility.Verify(ctx, p.data.backup, p.data.safety, stopped), ctx.Err())
}

func keepDataReview(value DataRestoreView) DataRestoreView {
	digest := sha256.Sum256([]byte("keep-current-data:" + value.ReviewID))
	value.ReviewID = hex.EncodeToString(digest[:])
	value.KeepCurrentData = true
	value.Changes = []RestoreChange{}

	return value
}
