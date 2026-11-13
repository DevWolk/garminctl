package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/gc"
)

type applicationRollbackFixture struct {
	f       *preflightNativeFixture
	request application.RollbackRequest
	old     *application.DataSnapshot
	account string
	commits int
}

func newApplicationRollbackFixture(t *testing.T) *applicationRollbackFixture {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)
	f := applicationPreflightFixture(t)
	oldPipeline, target := downloadPipeline(t, "v0.9.0", false)
	oldPipeline.stageDirectory, oldPipeline.packageDirectory = f.pipeline.stageDirectory, f.pipeline.packageDirectory

	old, err := oldPipeline.Download(t.Context(), application.Release{Tag: "v0.9.0"}, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	packages := applicationPackageStore{key: f.pipeline.key}
	currentCopy := openRestorePackage(t, packages, target, f.current.CacheID)
	oldCopy := openRestorePackage(t, packages, target, old.CacheID)
	account := gc.DefaultStorePath()
	writeApplicationState(t, filepath.Dir(account), filepath.Base(account), []byte(`{"opaque":"before upgrade"}`))

	snapshot, err := (applicationDataSnapshots{}).Create(t.Context(), application.PreflightInfo{Current: oldCopy.Record(), Update: currentCopy.Record()})
	if err != nil {
		t.Fatal(err)
	}

	writeApplicationState(t, filepath.Dir(account), filepath.Base(account), []byte(`{"opaque":"edited after upgrade"}`))
	writeApplicationState(t, filepath.Dir(account), filepath.Base(account)+".mfa", []byte(`{"opaque":"new MFA"}`))

	return &applicationRollbackFixture{f: f, account: account, old: snapshot, request: application.RollbackRequest{Package: application.PreflightRequest{Tag: old.Tag, CacheID: old.CacheID}, SnapshotID: snapshot.ID}}
}

func (f *applicationRollbackFixture) action(t *testing.T, fail bool) *application.RollbackAction {
	t.Helper()

	installer := applicationFixtureInstaller{prepare: func(_ context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
		if input.Info.Current.Tag != downloadTestBuild || input.Info.Update.Tag != f.request.Package.Tag {
			t.Fatal("wrong rollback package pair")
		}

		copyApplicationHandoffFixture(t, input)

		if got := readApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account)); string(got) != `{"opaque":"edited after upgrade"}` {
			t.Fatal("restore preceded installer payload preparation")
		}

		return applicationFixturePendingInstaller{commit: func(context.Context) error {
			f.commits++
			if err := f.f.backend.control.joinedFor(f.f.backend.address, true)(t.Context()); err != nil {
				t.Fatal(err)
			}

			if got := readApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account)); string(got) != `{"opaque":"before upgrade"}` {
				t.Fatal("installer committed before restored data")
			}

			if fail {
				return errors.New("synthetic commit reply lost")
			}

			return nil
		}, close: func() error { return nil }}, nil
	}}
	f.f.reader.handoff = application.NewInstallHandoffAction(f.f.reader.ApplicationReads, installer, f.f.backend.control.stopFor(f.f.backend.address, true))

	action, err := f.f.backend.control.rollbackAction(f.f.reader, f.f.backend.address, true)
	if err != nil {
		t.Fatal(err)
	}

	return action
}

func TestApplicationRollbackRestoresOnlyAfterOwnedJoinAndBeforeFakeCommit(t *testing.T) { //nolint:paralleltest // Production fixtures isolate process config/cache paths.
	applicationRollbackScenario(t, false)
}

func TestApplicationRollbackPreservesUncertainCommitAndBothBackups(t *testing.T) { //nolint:paralleltest // Production fixtures isolate process config/cache paths.
	applicationRollbackScenario(t, true)
}

func applicationRollbackScenario(t *testing.T, fail bool) {
	t.Helper()
	f := newApplicationRollbackFixture(t)
	action := f.action(t, fail)

	prepared, err := action.Prepare(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}

	closeRestoreResource(t, prepared)
	view := prepared.View()
	consent := application.RollbackConsent{Installation: application.InstallConsent{Confirmed: true, EditsResolved: true}, Data: application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: view.Data.ReviewID}}
	result, err := action.Run(t.Context(), prepared, consent)
	assertApplicationRollback(t, f, result, err, fail)

	reader, err := NewApplicationRecoverySnapshots()
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{view.Data.SafetySnapshotID, f.old.ID} {
		if _, err := reader.Verify(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}

	if got := readApplicationState(t, filepath.Join(filepath.Dir(f.account), "application-backups", view.Data.SafetySnapshotID, "config"), filepath.Base(f.account)); string(got) != `{"opaque":"edited after upgrade"}` {
		t.Fatal("newer edits missing from safety copy")
	}

	if again, err := action.Run(t.Context(), prepared, consent); err == nil || again != nil || f.commits != 1 {
		t.Fatalf("replayed=%+v %v", again, err)
	}
}

func assertApplicationRollback(t *testing.T, f *applicationRollbackFixture, result *application.RollbackResult, err error, fail bool) {
	t.Helper()

	if result == nil || result.Data == nil || !result.Data.Completed || (err != nil) != fail || f.commits != 1 {
		t.Fatalf("rollback=%+v %v commits=%d", result, err, f.commits)
	}

	want := application.HandoffAccepted
	if fail {
		want = application.HandoffCommitAttempted
	}

	if result.Handoff.State != want {
		t.Fatalf("handoff=%+v", result.Handoff)
	}

	if _, err := os.Stat(f.account + ".mfa"); !os.IsNotExist(err) {
		t.Fatalf("new MFA not removed: %v", err)
	}

	if f.f.external.Load() != 0 {
		t.Fatal("rollback fetched metadata")
	}
}
