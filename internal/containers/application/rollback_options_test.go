package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

func TestRollbackOptionsAreMetadataOnlyAndUseExactSnapshotPackage(t *testing.T) {
	t.Parallel()

	f := newRollbackFixture()
	old := cloneRecoverySnapshot(f.source.target)
	newer := cloneRecoverySnapshot(old)
	newer.ID = strings.Repeat("e", 64)
	newer.CreatedAt = newer.CreatedAt.Add(time.Hour)
	foreign := cloneRecoverySnapshot(old)
	foreign.ID = strings.Repeat("f", 64)
	foreign.Current.Digest = strings.Repeat("1", 64)
	source := &snapshotPolicyFixture{rows: []RecoverySnapshot{old, foreign, newer}}
	f.action.restore.snapshots = NewRecoverySnapshotsAction(source)

	rows, err := f.action.Options(t.Context(), false)
	if err != nil || len(rows) != 2 || rows[0].SnapshotID != newer.ID || rows[1].SnapshotID != old.ID {
		t.Fatalf("options=%+v %v", rows, err)
	}

	if source.verifies != 0 || len(f.packages.copies) != 0 || len(f.events) != 0 || f.gate.Snapshot().State != lifecycle.Running {
		t.Fatal("metadata listing performed preparation or verification")
	}

	rows[0].Package.Tag = "mutated-choice"
	if f.packages.rows[1].Tag != old.Current.Tag {
		t.Fatal("options alias catalog")
	}
}

func TestRollbackOptionsKeepPrereleaseOptInAndRejectAmbiguity(t *testing.T) {
	t.Parallel()

	f := newRollbackFixture()
	row := cloneRecoverySnapshot(f.source.target)
	row.Current.Tag = "v1.1.0-rc.1"
	row.Current.Prerelease = true
	f.packages.rows[1] = row.Current

	f.action.restore.snapshots = NewRecoverySnapshotsAction(&snapshotPolicyFixture{rows: []RecoverySnapshot{row}})
	if rows, err := f.action.Options(t.Context(), false); err != nil || len(rows) != 0 {
		t.Fatalf("default channel=%+v %v", rows, err)
	}

	if rows, err := f.action.Options(t.Context(), true); err != nil || len(rows) != 1 {
		t.Fatalf("explicit channel=%+v %v", rows, err)
	}

	f.packages.rows = append(f.packages.rows, row.Current)
	if rows, err := f.action.Options(t.Context(), true); err == nil || rows != nil {
		t.Fatalf("ambiguous options=%+v %v", rows, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if rows, err := f.action.Options(ctx, true); err == nil || rows != nil {
		t.Fatalf("canceled options=%+v %v", rows, err)
	}
}
