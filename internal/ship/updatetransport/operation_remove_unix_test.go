//go:build !windows

package updatetransport

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoveOperationRepeatsUntilTheDirectoryCanBeRemoved(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root removes entries of a read-only directory")
	}

	parent := t.TempDir()
	operation := filepath.Join(parent, "stage-1")

	if err := os.MkdirAll(filepath.Join(operation, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}

	// A read-only parent refuses the removal, like a file that is still locked.
	if err := os.Chmod(parent, 0o500); err != nil { //nolint:gosec // test directory, restored below
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) }) //nolint:gosec // test directory

	if err := os.RemoveAll(operation); err == nil {
		t.Fatal("the fixture does not refuse removal")
	}

	released := time.AfterFunc(10*removeOperationPause, func() { _ = os.Chmod(parent, 0o700) }) //nolint:gosec // test directory
	defer released.Stop()

	if err := removeOperation(operation); err != nil {
		t.Fatalf("removal after release: %v", err)
	}

	if _, err := os.Stat(operation); !os.IsNotExist(err) {
		t.Fatalf("operation directory remains: %v", err)
	}
}

func TestRemoveOperationReportsADirectoryThatStaysLocked(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root removes entries of a read-only directory")
	}

	parent := t.TempDir()
	operation := filepath.Join(parent, "stage-1")

	if err := os.Mkdir(operation, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(parent, 0o500); err != nil { //nolint:gosec // test directory, restored below
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) }) //nolint:gosec // test directory

	if err := removeOperation(operation); err == nil {
		t.Fatal("a directory that cannot be removed was reported as removed")
	}
}
