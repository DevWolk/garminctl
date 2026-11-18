package testdump

import (
	"bytes"
	"os"
	"testing"
)

// UpdateFixturesEnv makes CheckFixture rewrite generated fixtures when set to 1.
const UpdateFixturesEnv = "GARMINCTL_UPDATE_FIXTURES"

// CheckFixture compares a checked-in binary fixture with the bytes its
// generator test builds, so the file cannot drift from its definition.
// With GARMINCTL_UPDATE_FIXTURES=1 it writes the file instead.
func CheckFixture(tb testing.TB, path string, generated []byte) {
	tb.Helper()

	if os.Getenv(UpdateFixturesEnv) == "1" {
		if err := os.WriteFile(path, generated, 0o644); err != nil { //nolint:gosec // checked-in fixture named by its generator test, readable like other sources
			tb.Fatal(err)
		}

		return
	}

	current, err := os.ReadFile(path) //nolint:gosec // testdata fixture
	if err != nil {
		tb.Fatal(err)
	}

	if !bytes.Equal(current, generated) {
		tb.Fatalf("%s differs from its generator; rerun this test with %s=1", path, UpdateFixturesEnv)
	}
}
