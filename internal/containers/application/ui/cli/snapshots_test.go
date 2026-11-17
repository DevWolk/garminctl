package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

type snapshotCLIStore struct {
	row      application.RecoverySnapshot
	verifies int
}

func (s *snapshotCLIStore) List(context.Context) ([]application.RecoverySnapshot, error) {
	return []application.RecoverySnapshot{s.row}, nil
}

func (s *snapshotCLIStore) Verify(context.Context, string) (*application.RecoverySnapshot, error) {
	s.verifies++
	return &s.row, nil
}

func TestSnapshotCLISeparatesListingVerificationAndCompatibility(t *testing.T) {
	t.Parallel()

	for _, name := range []string{snapshotsCommand, inspectCommand} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			command, packages, _ := newRecoveryCommand(t, true)

			var out bytes.Buffer

			command.Out = clix.NewPrinter(&out)
			store := &snapshotCLIStore{row: application.RecoverySnapshot{ID: strings.Repeat("e", 64), CreatedAt: time.Now(), Current: packages.row, Update: packages.row, Files: []application.SnapshotFileRecord{{Area: "config", Path: "garmin-connect.json", Bytes: 20, Digest: strings.Repeat("d", 64)}}}}
			command.Snapshots = func() (*application.RecoverySnapshotsAction, error) {
				return application.NewRecoverySnapshotsAction(store), nil
			}

			args := []string{recoveryCommand, name}
			if name == inspectCommand {
				args = append(args, store.row.ID)
			}

			if err := command.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}

			assertSnapshotCommandResponse(t, name, out.Bytes(), store.verifies)

			if packages.calls != 0 {
				t.Fatalf("snapshot command used package/compiled-key composition: %d", packages.calls)
			}
		})
	}
}

func assertSnapshotCommandResponse(t *testing.T, name string, data []byte, verifies int) {
	t.Helper()

	var result struct {
		Verified          bool   `json:"verified"`
		DataCompatibility string `json:"dataCompatibility"`
		Verification      string `json:"verification"`
		Files             []any  `json:"files"`
		Snapshots         []any  `json:"snapshots"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	if name == snapshotsCommand && (result.Verified || len(result.Snapshots) != 1 || verifies != 0) {
		t.Fatalf("metadata=%s", data)
	}

	if name == inspectCommand && (!result.Verified || result.Verification != "local_manifest" || result.DataCompatibility != "not_checked" || len(result.Files) != 1 || verifies != 1) {
		t.Fatalf("inspection=%s count=%d", data, verifies)
	}
}

type snapshotCleanupStore struct {
	snapshotCLIStore
	unusable []application.UnusableEntry
	removed  []string
}

func (s *snapshotCleanupStore) Unusable(context.Context) ([]application.UnusableEntry, error) {
	return s.unusable, nil
}

func (s *snapshotCleanupStore) Remove(_ context.Context, id string) error {
	s.removed = append(s.removed, id)

	return nil
}

func TestSnapshotCLIWarnsAboutSkippedBackupsAndRemovesOne(t *testing.T) {
	t.Parallel()

	command, packages, out := newRecoveryCommand(t, false)

	var diagnostics bytes.Buffer

	command.ErrOut = clix.NewPrinter(&diagnostics)
	skipped := strings.Repeat("9", 64)
	store := &snapshotCleanupStore{unusable: []application.UnusableEntry{{ID: skipped, Reason: `json: unknown field "compression"`}}}
	store.row = application.RecoverySnapshot{ID: strings.Repeat("e", 64), CreatedAt: time.Now(), Current: packages.row, Update: packages.row}
	command.Snapshots = func() (*application.RecoverySnapshotsAction, error) {
		return application.NewRecoverySnapshotsAction(store), nil
	}

	if err := command.Run(t.Context(), []string{recoveryCommand, snapshotsCommand}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), store.row.ID) || strings.Contains(out.String(), skipped) || !strings.Contains(diagnostics.String(), "garminctl application recovery remove-snapshot "+skipped) || !strings.Contains(diagnostics.String(), "compression") {
		t.Fatalf("out=%q diagnostics=%q", out, &diagnostics)
	}

	out.Reset()

	if err := command.Run(t.Context(), []string{recoveryCommand, removeSnapshotCommand, skipped}); err != nil || len(store.removed) != 1 || store.removed[0] != skipped || !strings.Contains(out.String(), skipped) {
		t.Fatalf("removed=%v out=%q error=%v", store.removed, out, err)
	}

	if packages.calls != 0 {
		t.Fatalf("snapshot command used package composition: %d", packages.calls)
	}
}
