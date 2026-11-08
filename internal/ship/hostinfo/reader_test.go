package hostinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const testUnmanaged = "none"

func fixtureReader(t *testing.T) *Reader {
	t.Helper()
	directory := t.TempDir()
	desktop := filepath.Join(directory, "garmin-desktop")

	cli := filepath.Join(directory, "garminctl")
	for _, path := range []string{desktop, cli} {
		if err := os.WriteFile(path, []byte("fixture; never executable"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	reader := New(Config{DesktopPath: desktop, CLIPath: cli, PackageName: "garminctl", RegistryKey: "Software\\garminctl"})
	reader.platform = "linux"
	reader.arch = "amd64"
	reader.home = func() (string, error) { return directory, nil }
	reader.lookup = func(name string) (string, error) {
		if name == "dpkg-query" {
			return name, nil
		}

		return "", exec.ErrNotFound
	}
	reader.run = func(_ context.Context, kind probeCommand, path string) (string, error) {
		if kind == probeCLI {
			return "garminctl v1.2.0\n", nil
		}

		return "garminctl: " + path + "\n", nil
	}

	return reader
}

func TestReaderReportsDistinctRolesAndConfirmedOSOwners(t *testing.T) {
	t.Parallel()
	reader := fixtureReader(t)

	result, err := reader.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if result.Desktop.Version != "" || result.CLI.Version != "v1.2.0" || result.Desktop.Ownership.Manager != "dpkg" || result.CLI.Ownership.Package != "garminctl" {
		t.Fatalf("snapshot=%+v", result)
	}
}

func TestReaderDoesNotTreatToolFailureAsUnmanaged(t *testing.T) {
	t.Parallel()
	reader := fixtureReader(t)
	reader.run = func(_ context.Context, kind probeCommand, _ string) (string, error) {
		if kind == probeCLI {
			return "garminctl dev\n", nil
		}

		return "", errors.New("package database unavailable")
	}

	result, err := reader.Inspect(t.Context())
	if err != nil || result.CLI.Ownership.Manager == testUnmanaged || result.CLI.Ownership.Error == "" {
		t.Fatalf("snapshot=%+v error=%v", result, err)
	}
}

func TestReaderCallerCancellation(t *testing.T) {
	t.Parallel()
	reader := fixtureReader(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := reader.Inspect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
