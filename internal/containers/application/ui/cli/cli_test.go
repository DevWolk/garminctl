package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

type recoveryStore struct {
	calls int
	row   application.PackageRecord
	err   error
}

func (s *recoveryStore) List(context.Context) ([]application.PackageRecord, error) {
	s.calls++
	return []application.PackageRecord{s.row}, s.err
}

func (s *recoveryStore) Export(context.Context, string) (*application.RecoveryExport, error) {
	s.calls++
	return &application.RecoveryExport{Record: s.row, Directory: "/private/recovery", Path: "/private/recovery/pkg.deb", SignaturePath: "/private/recovery/pkg.deb.sig"}, s.err
}

func newRecoveryCommand(t *testing.T, asJSON bool) (*Command, *recoveryStore, *bytes.Buffer) {
	t.Helper()

	store := &recoveryStore{row: application.PackageRecord{ID: strings.Repeat("a", 64), Tag: "v1.2.0", Digest: strings.Repeat("b", 64), OS: "linux", Arch: "amd64", Installation: "deb", Bytes: 20}}

	var out, diagnostics bytes.Buffer

	command := &Command{Out: clix.NewPrinter(&out), ErrOut: clix.NewPrinter(&diagnostics), JSON: asJSON, Recovery: func() (*application.RecoveryAction, error) {
		return application.NewRecoveryAction(store, "linux", "amd64"), nil
	}}

	return command, store, &out
}

func TestRecoveryCLIDistinguishesMetadataAndVerifiedExport(t *testing.T) {
	t.Parallel()

	for _, command := range []string{listCommand, exportCommand} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			cli, store, out := newRecoveryCommand(t, true)

			args := []string{recoveryCommand, command}
			if command == exportCommand {
				args = append(args, store.row.ID)
			}

			if err := cli.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}

			var value struct {
				Verified bool   `json:"verified"`
				Path     string `json:"path"`
				Packages []struct {
					CacheID string `json:"cacheId"`
				} `json:"packages"`
			}
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}

			if command == listCommand && (value.Verified || len(value.Packages) != 1 || value.Packages[0].CacheID != store.row.ID || store.calls != 1) {
				t.Fatalf("metadata response=%s calls=%d", out, store.calls)
			}

			if command == exportCommand && (!value.Verified || value.Path != "/private/recovery/pkg.deb" || store.calls != 2) {
				t.Fatalf("export response=%s calls=%d", out, store.calls)
			}
		})
	}
}

func TestRecoveryCLIValidatesBeforeComposition(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {recoveryCommand}, {recoveryCommand, listCommand, "extra"}, {recoveryCommand, exportCommand}, {recoveryCommand, exportCommand, "../outside"}, {recoveryCommand, exportCommand, strings.Repeat("a", 64), "extra"}, {recoveryCommand, "install"}, {recoveryCommand, listCommand, "--public-key=foreign"}, {recoveryCommand, snapshotsCommand, "extra"}, {recoveryCommand, inspectCommand}, {recoveryCommand, inspectCommand, "../outside"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			t.Parallel()
			cli, _, _ := newRecoveryCommand(t, false)

			cli.Recovery = func() (*application.RecoveryAction, error) {
				t.Error("invalid args reached composition")
				return nil, errors.New("unexpected")
			}
			if err := cli.Run(t.Context(), args); clix.ExitCode(err) != 2 {
				t.Fatalf("usage error=%v", err)
			}
		})
	}
}

func TestRecoveryCLINeverReportsVerifiedOnFailure(t *testing.T) {
	t.Parallel()
	cli, store, out := newRecoveryCommand(t, true)

	store.err = errors.New("invalid signature")
	if err := cli.Run(t.Context(), []string{recoveryCommand, exportCommand, store.row.ID}); err == nil || out.Len() != 0 {
		t.Fatalf("failed export output=%q error=%v", out, err)
	}
}
