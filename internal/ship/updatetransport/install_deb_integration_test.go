//go:build integration && linux

package updatetransport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPrepareNativeInstallerWithPrivateDPKGFixtures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		target  string
		current string
		allow   bool
	}{
		{"prerelease to stable", debTestStable, "1.2.3.rc.1", true},
		{"stable to newer", debTestNewer, debTestStable, false},
		{"equal package versions", debTestStable, debTestStable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			directory, database := buildPrivateDEBFixture(t, tc.target, tc.current)
			query := func(ctx context.Context, program string, args ...string) (string, error) {
				if program == debQueryTool {
					args = append([]string{"--admindir=" + database}, args...)
				}

				return queryNativeVersion(ctx, program, args...)
			}
			request := installRequest{Target: MatchRequest{Installation: InstallDEB}, Release: ResolvedRelease{Package: AssetRef{Name: "fixture.deb"}}}

			got, err := prepareNativeInstaller(t.Context(), request, directory, query)
			if err != nil || slices.Contains(got.args, "--allow-downgrades") != tc.allow {
				t.Fatalf("native comparison invocation=%+v err=%v", got, err)
			}
		})
	}
}

func buildPrivateDEBFixture(t *testing.T, target, current string) (string, string) {
	t.Helper()

	directory := t.TempDir()
	control := filepath.Join(directory, "root", "DEBIAN")

	database := filepath.Join(directory, "db")
	for _, path := range []string{control, database} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Chmod(control, 0o755); err != nil { //nolint:gosec // dpkg-deb requires a traversable control directory; the inert fixture remains inside the private test directory.
		t.Fatal(err)
	}

	metadata := "Package: garminctl\nVersion: " + target + "\nArchitecture: all\nMaintainer: Fixture <fixture@example.invalid>\nDescription: inert comparison fixture\n"
	if err := os.WriteFile(filepath.Join(control, "control"), []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}

	status := "Package: garminctl\nStatus: install ok installed\nVersion: " + current + "\nArchitecture: all\nDescription: private database only\n\n"
	if err := os.WriteFile(filepath.Join(database, "status"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.CommandContext(t.Context(), debMetadataTool, "--build", filepath.Join(directory, "root"), filepath.Join(directory, "fixture.deb")) //nolint:gosec // Fixed dpkg-deb only builds an inert package under t.TempDir; no scripts or installation.
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build inert package: %v: %s", err, output)
	}

	return directory, database
}

func TestQueryNativeVersionBoundsAndCancellation(t *testing.T) {
	t.Parallel()

	if _, err := queryNativeVersion(t.Context(), "/usr/bin/printf", "%s", strings.Repeat("1", 9000)); err == nil {
		t.Fatal("oversized stdout accepted")
	}

	_, err := queryNativeVersion(t.Context(), debCompareTool, "--compare-versions", debTestNewer, "lt", debTestStable)

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("comparison false lost exit code: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err = queryNativeVersion(ctx, "/usr/bin/sleep", "30")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query deadline lost: %v", err)
	}
}
