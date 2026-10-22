// Package testdump builds a dump of a watch from the repository's fixtures
// for tests that drive garminctl over a directory instead of the MTP watch.
package testdump

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Root returns a temporary GARMIN tree holding GarminDevice.xml, OUT.BIN and
// Settings.fit from the garmin and fit testdata, with the folders a watch
// has. The test is skipped when a fixture is missing.
func Root(tb testing.TB) string {
	tb.Helper()

	_, self, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("testdump: cannot locate the repository")
	}

	internal := filepath.Dir(filepath.Dir(self))
	root := tb.TempDir()

	for _, d := range []string{"Apps", "Settings", "Activity", "Music"} {
		if err := os.MkdirAll(filepath.Join(root, "GARMIN", d), 0o750); err != nil {
			tb.Fatal(err)
		}
	}

	for src, dst := range map[string]string{
		filepath.Join("garmin", "testdata", "GarminDevice.xml"): filepath.Join("GARMIN", "GarminDevice.xml"),
		filepath.Join("garmin", "testdata", "OUT.BIN"):          filepath.Join("GARMIN", "Apps", "OUT.BIN"),
		filepath.Join("fit", "testdata", "Settings.fit"):        filepath.Join("GARMIN", "Settings", "Settings.fit"),
	} {
		b, err := os.ReadFile(filepath.Join(internal, src)) //nolint:gosec // testdata fixture
		if err != nil {
			tb.Skip("fixture missing:", err)
		}

		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil { //nolint:gosec // fixed names under the temp dir
			tb.Fatal(err)
		}
	}

	return root
}
