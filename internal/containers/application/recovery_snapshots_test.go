package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type snapshotPolicyFixture struct {
	rows     []RecoverySnapshot
	verifies int
	err      error
	result   *RecoverySnapshot
}

func (s *snapshotPolicyFixture) List(context.Context) ([]RecoverySnapshot, error) {
	return s.rows, s.err
}

func (s *snapshotPolicyFixture) Verify(context.Context, string) (*RecoverySnapshot, error) {
	s.verifies++
	return s.result, s.err
}

func snapshotPolicyRecord() RecoverySnapshot {
	return RecoverySnapshot{ID: strings.Repeat("a", 64), CreatedAt: time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC), Current: recoveryRecord("b", "v1.2.0"), Update: recoveryRecord("c", "v1.3.0"), Files: []SnapshotFileRecord{{Area: "config", Path: "garmin-connect.json", Bytes: 12, Digest: strings.Repeat("d", 64)}}, MissingSources: []string{"cache"}}
}

func TestRecoverySnapshotsListIsIndependentMetadataOnly(t *testing.T) {
	t.Parallel()

	row := snapshotPolicyRecord()
	source := &snapshotPolicyFixture{rows: []RecoverySnapshot{row}}

	values, err := NewRecoverySnapshotsAction(source).List(t.Context())
	if err != nil || len(values) != 1 || values[0].ID != row.ID || source.verifies != 0 {
		t.Fatalf("list=%+v verifies=%d error=%v", values, source.verifies, err)
	}

	values[0].Files[0].Path = "snapshot-mutated"

	values[0].MissingSources[0] = "snapshot-mutated"
	if source.rows[0].Files[0].Path != "garmin-connect.json" || source.rows[0].MissingSources[0] != "cache" {
		t.Fatal("snapshot metadata aliases storage")
	}
}

func TestRecoverySnapshotsVerifyRequiresIdentityAndSelectedState(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(*RecoverySnapshot)
	}{
		{name: "valid", change: func(*RecoverySnapshot) {}},
		{name: "other id", change: func(r *RecoverySnapshot) { r.ID = strings.Repeat("e", 64) }},
		{name: "unexpected data", change: func(r *RecoverySnapshot) { r.Files[0].Path = "application-packages/entry.json" }},
		{name: "unknown area", change: func(r *RecoverySnapshot) { r.Files[0].Area = "browser" }},
		{name: "invalid digest", change: func(r *RecoverySnapshot) { r.Files[0].Digest = "unknown" }},
		{name: "duplicate file", change: func(r *RecoverySnapshot) { r.Files = append(r.Files, r.Files[0]) }},
		{name: "foreign target", change: func(r *RecoverySnapshot) { r.Update.OS = "windows" }},
		{name: "unknown missing area", change: func(r *RecoverySnapshot) { r.MissingSources = []string{"private"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := snapshotPolicyRecord()
			id := row.ID
			tc.change(&row)
			source := &snapshotPolicyFixture{result: &row}

			value, err := NewRecoverySnapshotsAction(source).Verify(t.Context(), id)
			if tc.name != "valid" {
				if err == nil || value != nil {
					t.Fatalf("accepted snapshot=%+v error=%v", value, err)
				}

				return
			}

			if err != nil || value == nil || value.ID != id || source.verifies != 1 {
				t.Fatalf("verified=%+v count=%d error=%v", value, source.verifies, err)
			}

			value.Files[0].Path = "snapshot-mutated"
			if row.Files[0].Path != "garmin-connect.json" {
				t.Fatal("verified record aliases storage")
			}
		})
	}
}

func TestRecoverySnapshotsRejectsTraversalCancellationAndCorruption(t *testing.T) {
	t.Parallel()

	source := &snapshotPolicyFixture{result: new(RecoverySnapshot)}
	if value, err := NewRecoverySnapshotsAction(source).Verify(t.Context(), "../outside"); value != nil || err == nil || source.verifies != 0 {
		t.Fatalf("invalid snapshot=%+v count=%d error=%v", value, source.verifies, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if value, err := NewRecoverySnapshotsAction(source).Verify(ctx, strings.Repeat("a", 64)); value != nil || !errors.Is(err, context.Canceled) || source.verifies != 0 {
		t.Fatalf("canceled=%+v count=%d error=%v", value, source.verifies, err)
	}

	source.err = errors.New("snapshot digest mismatch")
	if value, err := NewRecoverySnapshotsAction(source).Verify(t.Context(), strings.Repeat("a", 64)); value != nil || !errors.Is(err, source.err) {
		t.Fatalf("corrupt=%+v error=%v", value, err)
	}
}
