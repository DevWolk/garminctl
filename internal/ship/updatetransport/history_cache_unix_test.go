//go:build !windows

package updatetransport_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestHistoryCacheUnixPermissions(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "history")

	cache, err := updatetransport.NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}

	if err := cache.Save(t.Context(), historySnapshot()); err != nil {
		t.Fatal(err)
	}
	// Deliberately permissive fixture checks that reads repair old permissions.
	if err := os.Chmod(directory, 0o750); err != nil { //nolint:gosec // permissive temporary fixture must be repaired by Load.
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Join(directory, "history.json"), 0o640); err != nil { //nolint:gosec // permissive temporary fixture must be repaired by Load.
		t.Fatal(err)
	}

	if _, err := cache.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{{directory, 0o700}, {filepath.Join(directory, "history.json"), 0o600}} {
		info, err := os.Stat(tc.path)
		if err != nil || info.Mode().Perm() != tc.mode {
			t.Fatalf("permissions %s: %v %v", tc.path, info, err)
		}
	}
}

func TestHistoryCacheUnixSymlinkRejection(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()

	cache, err := updatetransport.NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}

	if err := cache.Save(t.Context(), historySnapshot()); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()

	linked := filepath.Join(t.TempDir(), "history-link")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}

	bad, err := updatetransport.NewHistoryCache(linked)
	if err != nil {
		t.Fatal(err)
	}

	if err := bad.Save(t.Context(), historySnapshot()); err == nil {
		t.Fatal("accepted symlink directory")
	}

	if err := os.Remove(filepath.Join(directory, "history.json")); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(target, "outside.json")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(directory, "history.json")); err != nil {
		t.Fatal(err)
	}

	if result, err := cache.Load(t.Context()); result != nil || err == nil {
		t.Fatalf("accepted file link: %+v %v", result, err)
	}

	if data := readHistoryTestFile(t, outside); string(data) != "unchanged" {
		t.Fatalf("outside=%s", data)
	}
}
