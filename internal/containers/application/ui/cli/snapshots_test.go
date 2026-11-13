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
