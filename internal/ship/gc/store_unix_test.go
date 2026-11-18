//go:build !windows

package gc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRepairsPermissions(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o755); err != nil { //nolint:gosec // deliberately permissive test input, repaired by New
		t.Fatal(err)
	}

	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte(`{"email":"test@example.invalid","oauth1":{"oauth_token":"test-token"}}`), 0o644); err != nil { //nolint:gosec // synthetic permissive input must be repaired
		t.Fatal(err)
	}

	c := New(path)
	if err := c.StoreError(); err != nil {
		t.Fatal(err)
	}

	assertPermissions(t, dir, 0o700)
	assertPermissions(t, path, 0o600)

	if err := c.saveSession(testSession("new@example.invalid")); err != nil {
		t.Fatal(err)
	}

	assertPermissions(t, path, 0o600)
}

func TestStoreRejectsSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "other.json")

	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil { //nolint:gosec // synthetic target's permissions must stay unchanged
		t.Fatal(err)
	}

	path := filepath.Join(dir, "session.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if New(path).StoreError() == nil {
		t.Fatal("symlinked account store accepted")
	}

	assertPermissions(t, target, 0o644)
}

func assertPermissions(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := info.Mode().Perm(); got != want {
		t.Fatalf("permissions = %o, want %o", got, want)
	}
}
