//go:build !windows

package updatetransport_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackageCacheUnixPrivateFilesAndSymlinkRejection(t *testing.T) {
	t.Parallel()

	cache, entry, directory := retainedPackageForTest(t)
	for _, path := range []string{directory, filepath.Join(directory, entry.ID), filepath.Join(directory, entry.ID, "entry.json"), filepath.Join(directory, entry.ID, entry.Release.Package.Name), filepath.Join(directory, entry.ID, entry.Release.Signature.Name)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}

		if info.Mode().Perm() != want {
			t.Fatalf("mode %s=%o want=%o", path, info.Mode().Perm(), want)
		}
	}

	name := filepath.Join(directory, entry.ID, entry.Release.Package.Name)
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/dev/null", name); err != nil {
		t.Fatal(err)
	}

	if result, err := cache.Open(t.Context(), entry.ID, nil); result != nil || err == nil {
		t.Fatalf("accepted package symlink: %+v %v", result, err)
	}
}
