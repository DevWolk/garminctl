package app

import (
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

func TestNativeRollbackAdapterKeepsOneOwnedPreparationThroughConfirmation(t *testing.T) { //nolint:paralleltest // Fixture backend and cached CLI isolate process account/cache paths.
	f := newApplicationRollbackFixture(t)
	f.action(t, false)

	rows, err := f.f.reader.RetainedRollbacks(t.Context(), false)
	if err != nil || len(rows) != 1 || rows[0].SnapshotID != f.old.ID {
		t.Fatalf("native options=%+v %v", rows, err)
	}

	prepared, err := f.f.reader.PrepareRollback(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}

	closeRestoreResource(t, prepared)

	view := prepared.View()
	if f.commits != 0 || view.Data.TargetSnapshotID != f.old.ID || view.Data.SafetySnapshotID == "" {
		t.Fatalf("preparation=%+v commits=%d", view, f.commits)
	}

	if got := readApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account)); string(got) != `{"opaque":"edited after upgrade"}` {
		t.Fatal("review replaced live data")
	}

	result, err := prepared.Run(t.Context(), application.RollbackConsent{Installation: application.InstallConsent{Confirmed: true, EditsResolved: true}, Data: application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: view.Data.ReviewID}})
	assertApplicationRollback(t, f, result, err, false)
}
