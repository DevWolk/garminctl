//go:build integration && linux

package updatetransport

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
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
			request := installRequest{Target: MatchRequest{ReleaseTag: "v" + tc.target, Installation: InstallDEB}, Release: ResolvedRelease{Package: AssetRef{Name: "fixture.deb"}}}

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

// The privileged step reads the version with the real dpkg-deb from its own
// copy of a signed package, so a signed package of another release published
// under the selected release's name is refused before the package manager.
func TestPackageWorkerBindsRealPackageVersionToSelectedRelease(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, packaged string
		valid          bool
	}{
		{"selected release", packageTestVersion, true},
		{"older release under the selected name", debTestOlder, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			built, _ := buildPrivateDEBFixture(t, tc.packaged, debTestStable)

			data, err := os.ReadFile(filepath.Join(built, "fixture.deb")) //nolint:gosec // Inert package built above inside the test directory.
			if err != nil {
				t.Fatal(err)
			}

			config, request := signedPackageRequestFixture(t, data)
			request.AllowDowngrade = true

			run := &packageWorkerRun{}
			host := packageWorkerHost(t, "", run)
			host.query = queryNativeVersion

			err = runPackageWorker(t.Context(), config, packageWorkerInput(t, request), host)
			if (err == nil) != tc.valid || (len(run.invocations) == 1) != tc.valid {
				t.Fatalf("packaged %s: runs=%+v err=%v", tc.packaged, run.invocations, err)
			}

			if tc.valid && !slices.Contains(run.invocations[0].args, "--allow-downgrades") {
				t.Fatalf("rollback invocation=%+v", run.invocations[0])
			}

			if !tc.valid && !strings.Contains(err.Error(), tc.packaged) {
				t.Fatalf("refusal does not name the packaged version: %v", err)
			}
		})
	}
}

// signedPackageRequestFixture replaces the fixture package with the given
// bytes, signed by the same test key, as a verified download would hold them.
func signedPackageRequestFixture(t *testing.T, data []byte) (PackageWorkerConfig, packageRequest) {
	t.Helper()

	config, request, _ := packageWorkerFixture(t)

	key, err := installerTestKey()
	if err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(data)

	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(request.Directory, installTestPackage)
	replacePackageFixture(t, path, data)
	replacePackageFixture(t, path+".sig", signature)

	request.Size, request.Digest = int64(len(data)), hex.EncodeToString(digest[:])

	return config, request
}
