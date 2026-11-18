// Package testdump builds a dump of a watch from the repository's fixtures
// for tests that drive garminctl over a directory instead of the MTP watch.
package testdump

import (
	"os"
	"path/filepath"
	"testing"
)

// Root returns a temporary GARMIN tree holding the shared XML and registry
// fixtures and FIT Settings.fit, with the folders a watch has. A missing
// fixture fails the test.
func Root(tb testing.TB) string {
	tb.Helper()

	shared := filepath.Join(repositoryRoot(tb), "internal", "ship")
	root := tb.TempDir()

	for _, d := range []string{"Apps", "Settings", "Activity", "Music"} {
		if err := os.MkdirAll(filepath.Join(root, "GARMIN", d), 0o750); err != nil {
			tb.Fatal(err)
		}
	}

	for src, dst := range map[string]string{
		filepath.Join("testdump", "testdata", "GarminDevice.xml"): filepath.Join("GARMIN", "GarminDevice.xml"),
		filepath.Join("testdump", "testdata", "OUT.BIN"):          filepath.Join("GARMIN", "Apps", "OUT.BIN"),
		filepath.Join("fit", "testdata", "Settings.fit"):          filepath.Join("GARMIN", "Settings", "Settings.fit"),
	} {
		b, err := os.ReadFile(filepath.Join(shared, src)) //nolint:gosec // testdata fixture
		if err != nil {
			tb.Fatal("fixture missing:", err)
		}

		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil { //nolint:gosec // fixed names under the temp dir
			tb.Fatal(err)
		}
	}

	return root
}

// Locate fixtures from the test's working directory, including when a test
// binary was cross-compiled in Docker and runs on a Windows host.
func repositoryRoot(tb testing.TB) string {
	tb.Helper()

	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}

	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("testdump: cannot locate the repository from the working directory")
		}

		dir = parent
	}
}
