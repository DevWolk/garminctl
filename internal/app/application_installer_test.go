package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/hostfile"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

func applicationInstallerFixture(t *testing.T) (*preflightNativeFixture, *application.PreparedUpdate) {
	t.Helper()
	f := applicationPreflightFixture(t)
	// The observed CLI is a fixture binary, copied to its actual publishing basename.
	source := f.local.target.CLIPath
	root := t.TempDir()

	name := "garminctl"
	if filepath.Ext(source) == ".exe" {
		name += ".exe"
	}

	target := filepath.Join(root, name)
	if err := copyInstallerFixtureCLI(t.Context(), source, target); err != nil {
		t.Fatal(err)
	}

	f.local.target.CLIPath = target

	prepared, err := f.reader.Preflight(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := prepared.Close(); err != nil {
			t.Error(err)
		}
	})

	if _, err := f.reader.BackupData(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}

	return f, prepared
}

func copyInstallerFixtureCLI(ctx context.Context, source, target string) error {
	data, err := os.ReadFile(source) //nolint:gosec // Test executable reported by os.Executable in this isolated fixture.
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	root, err := os.OpenRoot(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer root.Close()

	if err := root.WriteFile(filepath.Base(target), data, 0o600); err != nil {
		return err
	}

	if err := hostfile.Protect(target, false); err != nil {
		return err
	}

	return os.Chmod(target, 0o700) //nolint:gosec // Owner-only executable fixture used only inside Docker/CI.
}

func TestApplicationInstallerReadyAbortOwnsIndependentVerifiedPayload(t *testing.T) { //nolint:paralleltest // Real cached CLI subprocess uses isolated process cache/config roots.
	f, prepared := applicationInstallerFixture(t)
	input := openApplicationInstallerCopies(t, f, prepared)

	pending, err := (applicationInstallerPort{key: f.pipeline.key, args: []string{"-root", "fixture-dump"}}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := pending.Close(); err != nil {
			t.Error(err)
		}
	}()

	if err := input.Current.Close(); err != nil {
		t.Fatal(err)
	}

	if err := input.Update.Close(); err != nil {
		t.Fatal(err)
	}

	operations, err := filepath.Glob(filepath.Join(applicationInstallCache(), "stage-*"))
	if err != nil || len(operations) != 1 {
		t.Fatalf("install operations=%v %v", operations, err)
	}

	if _, err := os.Stat(filepath.Join(operations[0], "worker.log")); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(operations[0], "install-result.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("installation ran without a Commit decision")
	}

	if err := pending.Close(); err != nil {
		t.Fatal(err)
	}

	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(operations[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("uncommitted helper/payload not removed")
	}

	f.assertClean(t)
}

func openApplicationInstallerCopies(t *testing.T, f *preflightNativeFixture, prepared *application.PreparedUpdate) application.InstallationInput {
	t.Helper()

	input := application.InstallationInput{Info: prepared.Info(), Snapshot: application.DataSnapshot{}}
	// The app port receives the same concrete verified copies as the real handoff.
	target := application.DownloadTarget{OS: input.Info.Inventory.OS, Arch: input.Info.Inventory.Arch, Installation: input.Info.Inventory.Installation.Kind, CLIPath: input.Info.Inventory.CLI.Path, CLIVersion: input.Info.Inventory.CLI.Version}
	store := applicationPackageStore{key: f.pipeline.key}

	current, err := store.Open(t.Context(), target, input.Info.Current.ID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := current.Close(); err != nil {
			t.Error(err)
		}
	})

	update, err := store.Open(t.Context(), target, input.Info.Update.ID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := update.Close(); err != nil {
			t.Error(err)
		}
	})

	input.Current, input.Update = current, update

	return input
}

func TestApplicationInstallerCancellationAndWorkerDiagnostics(t *testing.T) { //nolint:paralleltest // Real cached CLI workers use fixture-owned config/cache roots.
	f, prepared := applicationInstallerFixture(t)
	input := openApplicationInstallerCopies(t, f, prepared)
	port := applicationInstallerPort{key: f.pipeline.key}
	wrong := input
	wrong.Info.Inventory.CLI.Version = "different build"

	if pending, err := port.Prepare(t.Context(), wrong); err == nil || pending != nil || !strings.Contains(err.Error(), "installer build, key, protocol or parent does not match") {
		t.Fatalf("worker refusal=%v pending=%v", err, pending)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	pending, err := port.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	if err := pending.Close(); err != nil {
		t.Fatal(err)
	}

	operations, err := filepath.Glob(filepath.Join(applicationInstallCache(), "stage-*"))
	if err != nil || len(operations) != 0 {
		t.Fatalf("aborted helper not joined/cleaned: %v %v", operations, err)
	}

	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}

	if err := errors.Join(input.Current.Close(), input.Update.Close()); err != nil {
		t.Fatal(err)
	}

	f.assertClean(t)
}

func TestApplicationInstallerOwnedStopJoinsAndRejectsReused(t *testing.T) { //nolint:paralleltest // Production backend fixture isolates process account paths.
	f := serveApplicationBackend(t)
	if err := f.control.stopFor(f.address, false)(t.Context()); err == nil {
		t.Fatal("reused backend was stopped")
	}

	if err := f.control.stopFor(f.address, true)(t.Context()); err == nil {
		t.Fatal("stop admitted without maintenance")
	}

	release, err := f.control.maintenanceFor(f.address, true)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if err := f.control.stopFor(f.address, true)(t.Context()); err != nil {
		t.Fatal(err)
	}

	awaitApplicationBarrier(t, f.done)

	if f.runtime.Manager().Gate().Snapshot().State != lifecycle.Closed {
		t.Fatal("owned backend not fully stopped")
	}

	if err := f.control.stopFor(f.address, true)(t.Context()); !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("stale stop=%v", err)
	}
}
