//go:build !windows

package updatetransport

import (
	"os"
	"path/filepath"
	"testing"
)

// The first start of a freshly written program can take seconds while the
// system scans it; the installed files are already in place by then.
func TestInstalledVersionWaitsForASlowFirstStart(t *testing.T) {
	t.Parallel()

	cli := filepath.Join(t.TempDir(), "garminctl")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nsleep 4\necho 'garminctl v1.2.3'\n"), 0o700); err != nil { //nolint:gosec // test program
		t.Fatal(err)
	}

	got, err := installedVersion(t.Context(), cli)
	if err != nil || got != "v1.2.3" {
		t.Fatalf("version = %q, %v", got, err)
	}
}

func TestInstalledVersionRejectsOutputThatIsNotAVersionLine(t *testing.T) {
	t.Parallel()

	cli := filepath.Join(t.TempDir(), "garminctl")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\necho 'something else'\n"), 0o700); err != nil { //nolint:gosec // test program
		t.Fatal(err)
	}

	if got, err := installedVersion(t.Context(), cli); err == nil {
		t.Fatalf("accepted %q", got)
	}
}
