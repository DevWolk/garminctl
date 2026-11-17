package updatetransport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallToolsMustExistBeforeTheApplicationCloses(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	present := filepath.Join(directory, "present")
	plain := filepath.Join(directory, "not-executable")

	if err := os.WriteFile(present, []byte("#!/bin/true\n"), 0o700); err != nil { //nolint:gosec // Inert executable fixture inside the test directory.
		t.Fatal(err)
	}

	if err := os.WriteFile(plain, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := requireInstallTools(present); err != nil {
		t.Fatalf("present tool refused: %v", err)
	}

	for _, missing := range []string{filepath.Join(directory, "absent"), plain, directory} {
		err := requireInstallTools(present, missing)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("missing tool %s: %v", missing, err)
		}
	}
}

func TestPreflightNamesTheMissingLinuxTool(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(privilegeTool); err == nil {
		t.Skip("the authorization tool is installed")
	}

	_, request := installWorkerFixture(t)

	err := preflightNativeInstall(request, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), privilegeTool) {
		t.Fatalf("preflight without %s: %v", privilegeTool, err)
	}
}

func TestPrivilegedStepRunsOnlyASystemOwnedProgram(t *testing.T) {
	t.Parallel()

	if err := checkSystemProgram(debMetadataTool); err != nil {
		t.Fatalf("system tool refused: %v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("every fixture below would be owned by root")
	}

	own := filepath.Join(t.TempDir(), "garminctl")
	if err := os.WriteFile(own, []byte("fixture"), 0o700); err != nil { //nolint:gosec // Inert executable fixture inside the test directory.
		t.Fatal(err)
	}

	for _, path := range []string{own, "garminctl", filepath.Dir(debMetadataTool), debMetadataTool + "/../dpkg-deb", filepath.Join(t.TempDir(), "absent")} {
		if err := checkSystemProgram(path); err == nil {
			t.Fatalf("%s accepted for the privileged step", path)
		}
	}
}
