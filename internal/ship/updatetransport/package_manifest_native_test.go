//go:build nativeacceptance && linux

package updatetransport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/paths"
)

const nativeFixtureLinux = "linux"

// This explicit CI fixture generator runs in the Linux build container, not in
// shipped binaries. Offline catalog metadata names synthetic GitHub identities;
// no request is sent to them and native Open still uses the installed CLI's key.
func TestPrepareNativeAcceptancePackages(t *testing.T) {
	t.Parallel()

	if os.Getenv("NATIVE_PREVIOUS_VERSION") == "" {
		t.Skip("explicit native fixture versions are not configured")
	}

	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Fatal("native fixture generation requires an explicit CI context")
	}

	encoded, err := os.ReadFile("../../../build/update-public-key.b64")
	if err != nil {
		t.Fatal(err)
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}

	versions := []struct{ tag, directory string }{
		{os.Getenv("NATIVE_PREVIOUS_VERSION"), "../../../dist/previous"},
		{os.Getenv("NATIVE_SELECTED_VERSION"), "../../../dist/assets"},
	}
	if versions[0].tag == "" || versions[0].tag == versions[1].tag {
		t.Fatal("two distinct native fixture versions are required")
	}

	const output = "../../../dist/native-package-catalog"
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []InstallKind{InstallSetup, InstallDEB, InstallRPM} {
		platform := nativeFixtureLinux
		if kind == InstallSetup {
			platform = platformWindows
		}

		for _, version := range versions {
			target := MatchRequest{ReleaseTag: version.tag, OS: platform, Arch: "amd64", Installation: kind}

			entry, err := nativeAcceptancePackage(t.Context(), filepath.Join(output, string(kind)), version.directory, target, key)
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("retained %s %s: %s", kind, version.tag, entry.ID)
		}
	}
}

func nativeAcceptancePackage(ctx context.Context, directory, source string, target MatchRequest, key []byte) (_ *CachedPackage, err error) {
	name, err := packageFilename(target)
	if err != nil {
		return nil, err
	}

	identity, err := keyID(key)
	if err != nil {
		return nil, err
	}

	info, err := os.Lstat(filepath.Join(source, name))
	if err != nil {
		return nil, err
	}

	release := ResolvedRelease{
		ReleaseID: 1, Release: Release{Tag: target.ReleaseTag, Prerelease: strings.Contains(target.ReleaseTag, "-")},
		Package:   AssetRef{ID: 2, Name: name, Size: info.Size(), URL: releasesEndpoint + "/assets/2"},
		Signature: AssetRef{ID: 3, Name: name + ".sig", Size: packageSignatureSize, URL: releasesEndpoint + "/assets/3"},
	}
	if err := validateResolved(target, release); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}

	pending, err := os.MkdirTemp(directory, ".fixture-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(pending)) }()

	if err := copyPackagePair(ctx, source, pending, release); err != nil {
		return nil, err
	}

	digest, err := authenticateNativeFixture(ctx, pending, release, key)
	if err != nil {
		return nil, err
	}

	manifest, err := newPackageManifest(target, release, digest, identity)
	if err != nil {
		return nil, err
	}

	if err := writePackageManifest(pending, manifest); err != nil {
		return nil, err
	}

	if err := os.Rename(pending, filepath.Join(directory, manifest.ID)); err != nil {
		return nil, err
	}

	return &manifest.CachedPackage, nil
}

func authenticateNativeFixture(ctx context.Context, directory string, release ResolvedRelease, der []byte) (string, error) {
	key, err := parsePublicKey(der)
	if err != nil {
		return "", err
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()

	file, err := openPackageFile(root, release.Package.Name, release.Package.Size)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if err := copyPackage(ctx, hash, file, release.Package.Size, nil); err != nil {
		return "", err
	}

	signature, err := readStagedSignature(root, release.Signature.Name)
	if err != nil {
		return "", err
	}

	if err := verifyPackageSignature(ctx, key, hash.Sum(nil), signature); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func TestNativeAcceptancePackageUsesNormalOfflineCLI(t *testing.T) {
	config, request := installWorkerFixture(t)
	work := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(work, "cache"))
	cli := filepath.Join(work, "garminctl")
	flags := "-X main.version=" + config.BuildVersion + " -X main.updatePublicKey=" + base64.StdEncoding.EncodeToString(config.PublicKey)
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags", flags, "-o", cli, "./cmd/garminctl") // #nosec G204 -- fixed Go target; generated test key/version and output under t.TempDir.
	build.Dir = "../../.."

	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build normal CLI: %v\n%s", err, output)
	}

	directory := filepath.Join(paths.Cache(), "application-packages")

	entry, err := nativeAcceptancePackage(t.Context(), directory, config.Directory, request.Target, config.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	stager, err := NewStager(StagerConfig{CLIPath: cli, CacheDir: filepath.Join(paths.Cache(), "application-staging"), BuildVersion: config.BuildVersion, PublicKey: config.PublicKey})
	if err != nil {
		t.Fatal(err)
	}

	cache, err := NewPackageCache(directory, stager)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := cache.List(t.Context())
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("catalog: %+v %v", entries, err)
	}

	verified, err := cache.Open(t.Context(), entry.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	if verified.Digest() != entry.Digest {
		t.Errorf("offline digest=%s want=%s", verified.Digest(), entry.Digest)
	}

	if err := verified.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(directory, entry.ID, entry.Release.Signature.Name), make([]byte, packageSignatureSize), 0o600); err != nil {
		t.Fatal(err)
	}

	if result, err := cache.Open(t.Context(), entry.ID, nil); result != nil || err == nil {
		t.Fatalf("normal CLI accepted altered fixture signature: %v %v", result, err)
	}

	if _, err := os.Stat(filepath.Join(config.Directory, request.Release.Package.Name)); err != nil {
		t.Fatalf("fixture generation removed its source package: %v", err)
	}
}

func TestNativeAcceptancePackageRejectsAlteredFixtureBeforePublication(t *testing.T) {
	t.Parallel()

	config, request := installWorkerFixture(t)
	if err := os.WriteFile(filepath.Join(config.Directory, request.Release.Signature.Name), make([]byte, packageSignatureSize), 0o600); err != nil {
		t.Fatal(err)
	}

	directory := filepath.Join(t.TempDir(), "packages")

	entry, err := nativeAcceptancePackage(t.Context(), directory, config.Directory, request.Target, config.PublicKey)
	if entry != nil || err == nil {
		t.Fatalf("published altered native fixture: %v %v", entry, err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed fixture left catalog entries: %v %v", entries, err)
	}
}
