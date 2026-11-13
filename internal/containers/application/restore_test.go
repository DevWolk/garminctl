package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type restorePackage struct{ value PackageRecord }

func (p *restorePackage) Record() PackageRecord { return p.value }
func (*restorePackage) Close() error            { return nil }

type restoreSnapshots struct {
	target, safety RecoverySnapshot
	calls          int
}

func TestRestoreDataRefusesChangedBackupPackageAndForeignAction(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"retained bytes", "package identity", "other action"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			action, source, store, current, target := restorePolicyFixture()

			plan, err := action.Prepare(t.Context(), current, target, source.target.ID, source.safety.ID)
			if err != nil {
				t.Fatal(err)
			}

			if name == "retained bytes" {
				source.target.Files[0].Digest = strings.Repeat("f", 64)
			}

			if name == "package identity" {
				target.value.Tag = "v1.1.0"
			}

			if name == "other action" {
				action = NewRestoreDataAction(action.snapshots, store, action.stopped)
			}

			value, err := action.Run(t.Context(), plan, RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: plan.View().ReviewID})
			if value != nil || err == nil || store.calls != 0 {
				t.Fatalf("accepted %s=%+v calls=%d %v", name, value, store.calls, err)
			}
		})
	}
}

type blockingRestore struct {
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockingRestore) Restore(ctx context.Context, target, safety RecoverySnapshot) (*DataRestoreResult, error) {
	s.once.Do(func() { close(s.entered) })

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
	}

	return &DataRestoreResult{TargetSnapshotID: target.ID, SafetySnapshotID: safety.ID, Attempted: true, Completed: true, Replaced: 1, Removed: 1}, nil
}

func TestRestoreDataOnlyOneConfirmedPlanCanRun(t *testing.T) {
	t.Parallel()

	action, source, _, current, target := restorePolicyFixture()
	store := &blockingRestore{entered: make(chan struct{}), release: make(chan struct{})}
	action.store = store

	var release sync.Once
	defer release.Do(func() { close(store.release) })

	first, err := action.Prepare(t.Context(), current, target, source.target.ID, source.safety.ID)
	if err != nil {
		t.Fatal(err)
	}

	second, err := action.Prepare(t.Context(), current, target, source.target.ID, source.safety.ID)
	if err != nil {
		t.Fatal(err)
	}

	consent := RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: first.View().ReviewID}
	done := make(chan error, 1)

	go func() { _, err := action.Run(t.Context(), first, consent); done <- err }()

	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-store.entered:
	}

	if value, err := action.Run(t.Context(), second, consent); value != nil || !errors.Is(err, ErrPreparationBusy) {
		t.Fatalf("parallel restore=%+v %v", value, err)
	}

	release.Do(func() { close(store.release) })

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (s *restoreSnapshots) List(context.Context) ([]RecoverySnapshot, error) {
	return nil, errors.New("listing is not needed")
}

func (s *restoreSnapshots) Verify(_ context.Context, id string) (*RecoverySnapshot, error) {
	s.calls++
	if id == s.target.ID {
		v := cloneRecoverySnapshot(s.target)
		return &v, nil
	}

	v := cloneRecoverySnapshot(s.safety)

	return &v, nil
}

type restoreEffect struct {
	calls int
	err   error
}

func (s *restoreEffect) Restore(_ context.Context, target, safety RecoverySnapshot) (*DataRestoreResult, error) {
	s.calls++
	return &DataRestoreResult{TargetSnapshotID: target.ID, SafetySnapshotID: safety.ID, Attempted: true, Completed: s.err == nil, Replaced: 1, Removed: 1}, s.err
}

func restorePolicyFixture() (*RestoreDataAction, *restoreSnapshots, *restoreEffect, *restorePackage, *restorePackage) {
	old, newer := recoveryRecord("a", "v1.2.0"), recoveryRecord("b", "v1.3.0")
	backup, safety := snapshotPolicyRecord(), snapshotPolicyRecord()
	backup.ID = strings.Repeat("c", 64)
	backup.Current = old
	backup.Update = newer
	safety.ID = strings.Repeat("d", 64)
	safety.Current = newer
	safety.Update = old
	safety.Files[0].Digest = strings.Repeat("e", 64)
	safety.Files = append(safety.Files, SnapshotFileRecord{Area: "config", Path: "garmin-connect.json.mfa", Bytes: 9, Digest: strings.Repeat("f", 64)})
	source := &restoreSnapshots{target: backup, safety: safety}
	store := new(restoreEffect)

	return NewRestoreDataAction(NewRecoverySnapshotsAction(source), store, func(context.Context) error { return nil }), source, store, &restorePackage{newer}, &restorePackage{old}
}

func TestRestoreDataRequiresExactReviewAndJoinedOwner(t *testing.T) {
	t.Parallel()

	action, source, store, current, target := restorePolicyFixture()

	plan, err := action.Prepare(t.Context(), current, target, source.target.ID, source.safety.ID)
	if err != nil || plan == nil {
		t.Fatalf("prepare=%+v %v", plan, err)
	}

	view := plan.View()
	if len(view.Changes) != 2 || view.ReviewID == "" || store.calls != 0 {
		t.Fatalf("review=%+v writes=%d", view, store.calls)
	}

	view.Changes[0].Path = "altered"
	if plan.View().Changes[0].Path == "altered" {
		t.Fatal("review aliases plan")
	}

	assertRestoreRefused(t, action, plan, RestoreConsent{}, store)
	assertRestoreRefused(t, action, plan, RestoreConsent{EditsResolved: true, ReviewID: plan.View().ReviewID}, store)
	assertRestoreRefused(t, action, plan, RestoreConsent{Confirmed: true, ReviewID: plan.View().ReviewID}, store)
	assertRestoreRefused(t, action, plan, RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: "other"}, store)

	action.stopped = func(context.Context) error { return errors.New("backend is live") }
	assertRestoreRefused(t, action, plan, RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: plan.View().ReviewID}, store)
}

func assertRestoreRefused(t *testing.T, action *RestoreDataAction, plan *DataRestorePlan, consent RestoreConsent, store *restoreEffect) {
	t.Helper()

	if result, err := action.Run(t.Context(), plan, consent); result != nil || err == nil || store.calls != 0 {
		t.Fatalf("restore refusal=%+v writes=%d error=%v", result, store.calls, err)
	}
}

func TestRestoreDataConsumesConfirmedPlanAndPreservesFailureReceipt(t *testing.T) {
	t.Parallel()

	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial failure"}[fail], func(t *testing.T) {
			t.Parallel()

			action, source, store, current, target := restorePolicyFixture()
			if fail {
				store.err = errors.New("second file failed")
			}

			plan, err := action.Prepare(t.Context(), current, target, source.target.ID, source.safety.ID)
			if err != nil {
				t.Fatal(err)
			}

			consent := RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: plan.View().ReviewID}

			result, err := action.Run(t.Context(), plan, consent)
			if result == nil || !result.Attempted || result.Completed == fail || (err != nil) != fail || store.calls != 1 {
				t.Fatalf("result=%+v count=%d %v", result, store.calls, err)
			}

			if again, err := action.Run(t.Context(), plan, consent); again != nil || err == nil || store.calls != 1 {
				t.Fatalf("replayed=%+v count=%d %v", again, store.calls, err)
			}
		})
	}
}
