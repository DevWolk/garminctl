package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"

	"golang.org/x/mod/semver"
)

// RestoreChange is a reviewed replacement or removal of selected local data.
type RestoreChange struct {
	Area, Path, Operation string
	Bytes                 int64
}

// DataRestoreView describes explicit backup restoration, not schema compatibility.
type DataRestoreView struct {
	ReviewID, TargetSnapshotID, SafetySnapshotID string
	From, To                                     string
	Changes                                      []RestoreChange
}

// DataRestoreResult preserves an attempted operation even when it fails.
type DataRestoreResult struct {
	TargetSnapshotID, SafetySnapshotID string
	Attempted, Completed               bool
	Replaced, Removed                  int
}

// RestoreConsent binds explicit consent to the exact reviewed file set.
type RestoreConsent struct {
	Confirmed, EditsResolved bool
	ReviewID                 string
}

// DataRestorations rechecks retained and live bytes at the mutation boundary.
type DataRestorations interface {
	Restore(context.Context, RecoverySnapshot, RecoverySnapshot) (*DataRestoreResult, error)
}

// DataRestorePlan is action-owned, single-use and never reconstructed from JSON.
// The caller keeps both verified packages alive until Run returns.
type DataRestorePlan struct {
	owner           *RestoreDataAction
	current, target VerifiedPackage
	safety, backup  RecoverySnapshot
	view            DataRestoreView
	used            bool
}

// RestoreDataAction restores only after the same owned backend has joined.
type RestoreDataAction struct {
	snapshots *RecoverySnapshotsAction
	store     DataRestorations
	stopped   func(context.Context) error
	mu        sync.Mutex
}

// NewRestoreDataAction creates no runtime, file or installer authority.
func NewRestoreDataAction(snapshots *RecoverySnapshotsAction, store DataRestorations, stopped func(context.Context) error) *RestoreDataAction {
	return &RestoreDataAction{snapshots: snapshots, store: store, stopped: stopped}
}

// View returns independent display data; it cannot grant restore permission.
func (p *DataRestorePlan) View() DataRestoreView {
	value := p.view
	value.Changes = slices.Clone(value.Changes)

	return value
}

// Prepare verifies both local backups and their borrowed signed-package identities.
func (a *RestoreDataAction) Prepare(ctx context.Context, current, target VerifiedPackage, backupID, safetyID string) (*DataRestorePlan, error) {
	if err := a.check(ctx); err != nil {
		return nil, err
	}

	if current == nil || target == nil || backupID == safetyID {
		return nil, errors.New("restore requires a verified package pair and distinct backups")
	}

	from, to := current.Record(), target.Record()
	if err := validateRestorePackages(from, to); err != nil {
		return nil, err
	}

	backup, err := a.snapshots.Verify(ctx, backupID)
	if err != nil {
		return nil, err
	}

	safety, err := a.snapshots.Verify(ctx, safetyID)
	if err != nil {
		return nil, err
	}

	if backup.Current != to || safety.Current != from || safety.Update != to {
		return nil, errors.New("backups do not belong to this rollback package pair")
	}

	if err := validateRestoreNames(backup.Files, safety.Files); err != nil {
		return nil, err
	}

	changes := restoreChanges(backup.Files, safety.Files)

	data, err := json.Marshal(struct{ Backup, Safety RecoverySnapshot }{*backup, *safety})
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256(data)
	view := DataRestoreView{ReviewID: hex.EncodeToString(digest[:]), TargetSnapshotID: backup.ID, SafetySnapshotID: safety.ID, From: from.Tag, To: to.Tag, Changes: changes}

	return &DataRestorePlan{owner: a, current: current, target: target, backup: *backup, safety: *safety, view: view}, nil
}

// Run consumes one confirmed plan, refusing a live/reused backend or stale data.
func (a *RestoreDataAction) Run(ctx context.Context, plan *DataRestorePlan, consent RestoreConsent) (*DataRestoreResult, error) {
	if err := a.check(ctx); err != nil {
		return nil, err
	}

	if plan == nil || plan.owner != a || !consent.Confirmed || !consent.EditsResolved || consent.ReviewID != plan.view.ReviewID {
		return nil, errors.New("confirm the exact restore review and resolve user edits")
	}

	if !a.mu.TryLock() {
		return nil, ErrPreparationBusy
	}
	defer a.mu.Unlock()

	if plan.used {
		return nil, errors.New("restore plan was already consumed; inspect and prepare again")
	}

	plan.used = true
	if plan.current.Record() != plan.safety.Current || plan.target.Record() != plan.backup.Current {
		return nil, errors.New("verified package identity changed after restore review")
	}

	if err := a.stopped(ctx); err != nil {
		return nil, err
	}

	if err := a.recheck(ctx, plan); err != nil {
		return nil, err
	}

	return a.restore(ctx, plan)
}

func (a *RestoreDataAction) restore(ctx context.Context, plan *DataRestorePlan) (*DataRestoreResult, error) {
	result := &DataRestoreResult{TargetSnapshotID: plan.backup.ID, SafetySnapshotID: plan.safety.ID, Attempted: true}

	effect, err := a.store.Restore(ctx, plan.backup, plan.safety)
	if effect != nil {
		result.Replaced, result.Removed = effect.Replaced, effect.Removed

		result.Completed = effect.Completed && err == nil
		if effect.TargetSnapshotID != result.TargetSnapshotID || effect.SafetySnapshotID != result.SafetySnapshotID || effect.Replaced < 0 || effect.Removed < 0 || effect.Replaced+effect.Removed > len(plan.view.Changes) {
			err = errors.Join(err, errors.New("restore returned an invalid effect receipt"))
			result.Completed = false
		}
	}

	if !result.Completed && err == nil {
		err = errors.New("restore did not confirm completion")
	}

	return result, errors.Join(err, ctx.Err())
}

func (a *RestoreDataAction) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if a.snapshots == nil || a.store == nil || a.stopped == nil {
		return errors.New("explicit restore is not configured")
	}

	return nil
}

func (a *RestoreDataAction) recheck(ctx context.Context, plan *DataRestorePlan) error {
	for _, expected := range []RecoverySnapshot{plan.backup, plan.safety} {
		actual, err := a.snapshots.Verify(ctx, expected.ID)
		if err != nil {
			return err
		}

		if !reflect.DeepEqual(*actual, expected) {
			return errors.New("retained backup changed after restore review")
		}
	}

	return ctx.Err()
}

func validateRestorePackages(current, target PackageRecord) error {
	platform := DownloadTarget{OS: current.OS, Arch: current.Arch, Installation: current.Installation}
	if err := validatePreflightPackage(current, platform, true); err != nil {
		return err
	}

	if err := validatePreflightPackage(target, platform, true); err != nil {
		return err
	}

	if semver.Compare(semanticVersion(target.Tag), semanticVersion(current.Tag)) >= 0 {
		return errors.New("rollback restore requires an older verified package")
	}

	return nil
}

func restoreChanges(target, current []SnapshotFileRecord) []RestoreChange {
	old := make(map[string]SnapshotFileRecord, len(current))
	for _, file := range current {
		old[file.Area+"/"+file.Path] = file
	}

	result := make([]RestoreChange, 0)

	for _, file := range target {
		key := file.Area + "/" + file.Path
		before, ok := old[key]
		delete(old, key)

		if !ok || before.Digest != file.Digest || before.Bytes != file.Bytes {
			result = append(result, RestoreChange{Area: file.Area, Path: file.Path, Operation: "replace", Bytes: file.Bytes})
		}
	}

	for _, file := range old {
		result = append(result, RestoreChange{Area: file.Area, Path: file.Path, Operation: "remove", Bytes: file.Bytes})
	}

	slices.SortFunc(result, func(a, b RestoreChange) int { return strings.Compare(a.Area+"/"+a.Path, b.Area+"/"+b.Path) })

	return result
}

func validateRestoreNames(target, current []SnapshotFileRecord) error {
	names := make(map[string]string)

	for _, files := range [][]SnapshotFileRecord{target, current} {
		for _, file := range files {
			name := file.Area + "/" + file.Path

			key := strings.ToLower(name)
			if prior, ok := names[key]; ok && prior != name {
				return errors.New("restore paths differ only by case")
			}

			names[key] = name
		}
	}

	return nil
}
