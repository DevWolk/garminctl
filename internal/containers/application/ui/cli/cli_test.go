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

	for _, args := range [][]string{nil, {recoveryCommand}, {recoveryCommand, removePackageCommand}, {recoveryCommand, removePackageCommand, "../outside"}, {recoveryCommand, removeSnapshotCommand}, {recoveryCommand, listCommand, "extra"}, {recoveryCommand, exportCommand}, {recoveryCommand, exportCommand, "../outside"}, {recoveryCommand, exportCommand, strings.Repeat("a", 64), "extra"}, {recoveryCommand, "install"}, {recoveryCommand, listCommand, "--public-key=foreign"}, {recoveryCommand, snapshotsCommand, "extra"}, {recoveryCommand, inspectCommand}, {recoveryCommand, inspectCommand, "../outside"}} {
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

type cleanupStore struct {
	recoveryStore
	unusable []application.UnusableEntry
	removed  []string
}

func (s *cleanupStore) Unusable(context.Context) ([]application.UnusableEntry, error) {
	return s.unusable, nil
}

func (s *cleanupStore) Remove(_ context.Context, id string) error {
	s.removed = append(s.removed, id)

	return s.err
}

// skippedPackageCommand lists one usable package beside one the store skipped.
func skippedPackageCommand(t *testing.T, asJSON bool) (*Command, *cleanupStore, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	cli, base, out := newRecoveryCommand(t, asJSON)
	store := &cleanupStore{unusable: []application.UnusableEntry{{ID: strings.Repeat("9", 64), Reason: "package manifest schema, source or compiled key does not match"}}}
	store.row = base.row
	diagnostics := new(bytes.Buffer)

	cli.ErrOut = clix.NewPrinter(diagnostics)
	cli.Recovery = func() (*application.RecoveryAction, error) {
		return application.NewRecoveryAction(store, "linux", "amd64"), nil
	}

	return cli, store, out, diagnostics
}

func TestRecoveryCLIWarnsAboutSkippedPackages(t *testing.T) {
	t.Parallel()

	cli, store, out, diagnostics := skippedPackageCommand(t, false)
	skipped := store.unusable[0].ID

	if err := cli.Run(t.Context(), []string{recoveryCommand, listCommand}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), store.row.ID) || strings.Contains(out.String(), skipped) || !strings.Contains(diagnostics.String(), "garminctl application recovery remove-package "+skipped) {
		t.Fatalf("out=%q diagnostics=%q", out, diagnostics)
	}

	cli, _, out, diagnostics = skippedPackageCommand(t, true)
	if err := cli.Run(t.Context(), []string{recoveryCommand, listCommand}); err != nil {
		t.Fatal(err)
	}

	var value struct {
		Packages []any                         `json:"packages"`
		Unusable []struct{ ID, Reason string } `json:"unusable"`
	}
	if err := json.Unmarshal(out.Bytes(), &value); err != nil || len(value.Packages) != 1 || len(value.Unusable) != 1 || value.Unusable[0].ID != skipped || value.Unusable[0].Reason == "" || diagnostics.Len() != 0 {
		t.Fatalf("json=%s diagnostics=%q error=%v", out, diagnostics, err)
	}
}

func TestRecoveryCLIRemovesOnePackageAndReportsAFailedRemoval(t *testing.T) {
	t.Parallel()

	cli, store, out, _ := skippedPackageCommand(t, false)
	skipped := store.unusable[0].ID

	if err := cli.Run(t.Context(), []string{recoveryCommand, removePackageCommand, skipped}); err != nil || len(store.removed) != 1 || store.removed[0] != skipped || !strings.Contains(out.String(), skipped) {
		t.Fatalf("removed=%v out=%q error=%v", store.removed, out, err)
	}

	store.err = errors.New("the selected package is not retained")

	out.Reset()

	if err := cli.Run(t.Context(), []string{recoveryCommand, removePackageCommand, skipped}); err == nil || out.Len() != 0 {
		t.Fatalf("failed removal out=%q error=%v", out, err)
	}
}
