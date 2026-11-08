package updatetransport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogManifest(t *testing.T) (packageManifest, *Stager) {
	t.Helper()

	request := MatchRequest{ReleaseTag: "v1.2.0", OS: "linux", Arch: "amd64", Installation: InstallDEB}
	release := ResolvedRelease{
		ReleaseID: 41, Release: Release{Tag: request.ReleaseTag},
		Package:   AssetRef{ID: 101, Name: "garminctl_1.2.0_linux_amd64.deb", Size: 1, URL: releasesEndpoint + "/assets/101"},
		Signature: AssetRef{ID: 708, Name: "garminctl_1.2.0_linux_amd64.deb.sig", Size: 64, URL: releasesEndpoint + "/assets/708"},
	}
	owner := &Stager{cacheDir: filepath.Join(t.TempDir(), "stage"), key: strings.Repeat("a", 64)}

	manifest, err := newPackageManifest(request, release, strings.Repeat("b", 64), owner.key)
	if err != nil {
		t.Fatal(err)
	}

	return manifest, owner
}

func TestPackageManifestRejectsUntrustedDataBeforeVerification(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		edit  func(string) string
		valid bool
	}{
		{name: "valid", edit: func(s string) string { return s }, valid: true},
		{"schema", func(s string) string { return strings.Replace(s, `"version":1`, `"version":2`, 1) }, false},
		{"source", func(s string) string {
			return strings.Replace(s, `"source":"`+releasesEndpoint+`"`, `"source":"other"`, 1)
		}, false},
		{"key identity", func(s string) string { return strings.ReplaceAll(s, strings.Repeat("a", 64), strings.Repeat("c", 64)) }, false},
		{"unknown key override", func(s string) string { return strings.Replace(s, `"version":1`, `"publicKey":"other","version":1`, 1) }, false},
		{"trailing", func(s string) string { return s + " {}" }, false},
		{"truncated", func(s string) string { return s[:len(s)-1] }, false},
		{"overlong", func(s string) string { return s + strings.Repeat(" ", maxStageRequest) }, false},
		{"asset name", func(s string) string { return strings.ReplaceAll(s, "garminctl_1.2.0_linux_amd64.deb", "../outside") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manifest, owner := catalogManifest(t)
			directory := t.TempDir()

			cache, err := NewPackageCache(directory, owner)
			if err != nil {
				t.Fatal(err)
			}

			entry := filepath.Join(directory, manifest.ID)
			if err := os.Mkdir(entry, 0o700); err != nil {
				t.Fatal(err)
			}

			root, err := os.OpenRoot(entry)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}

			if err := root.WriteFile(packageManifestFile, []byte(tc.edit(string(data))), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := cache.readManifest(t.Context(), manifest.ID); (err == nil) != tc.valid {
				t.Fatalf("manifest valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

func TestPackageCatalogMetadataCannotChooseHostPath(t *testing.T) {
	t.Parallel()
	_, owner := catalogManifest(t)

	cache, err := NewPackageCache(t.TempDir(), owner)
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"", "../outside", "/etc/passwd", strings.Repeat("A", 64)} {
		if result, err := cache.Open(t.Context(), id, nil); result != nil || err == nil || !strings.Contains(err.Error(), "invalid package catalog identity") {
			t.Fatalf("accepted catalog path %q", id)
		}
	}
}
