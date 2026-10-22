// Package paths names the directories garminctl keeps its own files in.
package paths

import (
	"os"
	"path/filepath"
)

// Cache is garminctl's cache directory: the user's cache directory, or the
// temp directory when the platform has none, plus "garminctl". Callers
// create it with mode 0700; it holds the bridge script and per-watch caches.
func Cache() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}

	return filepath.Join(base, "garminctl")
}
