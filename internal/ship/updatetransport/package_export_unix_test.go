//go:build !windows

package updatetransport_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackageExportPrivateFilesAndSymlinkRejection(t *testing.T) {
	t.Parallel()
	cache, entry, _ := retainedPackageForTest(t)
	directory := filepath.Join(t.TempDir(), "recovery")

	value, err := cache.Export(t.Context(), entry.ID, directory, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{directory, value.Directory, value.Path, value.SignaturePath} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}

		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}

		if info.Mode().Perm() != want {
			t.Fatalf("permissions %s=%o want=%o", path, info.Mode().Perm(), want)
		}
	}

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}

	if result, err := cache.Export(t.Context(), entry.ID, alias, nil); result != nil || err == nil {
		t.Fatalf("accepted export symlink=%+v error=%v", result, err)
	}
}
