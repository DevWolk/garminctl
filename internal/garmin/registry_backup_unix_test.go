//go:build !windows

package garmin

import (
	"os"
	"path/filepath"
	"testing"
)

func assertRegistryBackupProtection(t *testing.T, path string) {
	t.Helper()

	for name, want := range map[string]os.FileMode{
		path: 0o600, filepath.Dir(path): 0o700, filepath.Dir(filepath.Dir(path)): 0o700,
	} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}

		if info.Mode().Perm() != want {
			t.Fatalf("%s mode = %o, want %o", name, info.Mode().Perm(), want)
		}
	}
}
