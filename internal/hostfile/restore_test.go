package hostfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type restoreFileFixture struct {
	restorer       *SnapshotRestorer
	target, safety *Snapshot
	live           *os.Root
}

func newRestoreFileFixture(t *testing.T) restoreFileFixture {
	t.Helper()
	return restoreFileFixtureMFA(t, false)
}

func restoreFileFixtureMFA(t *testing.T, oldMFA bool) restoreFileFixture {
	t.Helper()
	root := t.TempDir()
	live := filepath.Join(root, "live")
	backups := filepath.Join(root, "backups")

	if err := os.Mkdir(live, 0o700); err != nil {
		t.Fatal(err)
	}

	fs, err := os.OpenRoot(live)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	selectPath := func(_, path string) SnapshotKind {
		if path == "account.json" || path == "mfa.json" {
			return SnapshotFile
		}

		return SnapshotIgnore
	}
	store := NewSnapshotStore(backups, []SnapshotSource{{Name: "config", Directory: live}}, selectPath)

	if err := fs.WriteFile("account.json", []byte("old account"), 0o600); err != nil {
		t.Fatal(err)
	}

	if oldMFA {
		if err := fs.WriteFile("mfa.json", []byte("old pending MFA"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	target, err := store.Create(t.Context(), json.RawMessage(`{"version":"old"}`))
	if err != nil {
		t.Fatal(err)
	}

	if err := fs.WriteFile("account.json", []byte("new account"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fs.WriteFile("mfa.json", []byte("new pending MFA"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fs.WriteFile("unselected.txt", []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}

	safety, err := store.Create(t.Context(), json.RawMessage(`{"version":"new"}`))
	if err != nil {
		t.Fatal(err)
	}

	return restoreFileFixture{restorer: NewSnapshotRestorer(backups, []SnapshotSource{{Name: "config", Directory: live}}, selectPath), target: target, safety: safety, live: fs}
}

func TestSnapshotRestorePartialFailureIsReportedWithoutUndoOrBackupRemoval(t *testing.T) {
	t.Parallel()
	f := restoreFileFixtureMFA(t, true)
	calls := 0
	failure := errors.New("injected second replacement failure")
	f.restorer.replace = func(from, to string) error {
		calls++
		if calls == 2 {
			return failure
		}

		return Replace(from, to)
	}

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if result == nil || !result.Attempted || result.Completed || result.Replaced != 1 || !errors.Is(err, failure) || calls != 2 {
		t.Fatalf("partial=%+v calls=%d error=%v", result, calls, err)
	}

	if data, err := f.live.ReadFile("account.json"); err != nil || string(data) != "old account" {
		t.Fatalf("first effect hidden/undone=%q %v", data, err)
	}

	if data, err := f.live.ReadFile("mfa.json"); err != nil || string(data) != "new pending MFA" {
		t.Fatalf("second effect=%q %v", data, err)
	}

	assertRestoreCopiesAndCleanup(t, f)
}

func assertRestoreCopiesAndCleanup(t *testing.T, f restoreFileFixture) {
	t.Helper()

	for _, id := range []string{f.target.ID, f.safety.ID} {
		if _, err := f.restorer.reader.Open(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := fs.ReadDir(f.live.FS(), ".")
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".garminctl-restore-") {
			t.Fatalf("staging leaked after ordinary failure: %s", entry.Name())
		}
	}
}

func TestSnapshotRestoreRechecksEachFileAfterAnEarlierReplacement(t *testing.T) {
	t.Parallel()
	f := restoreFileFixtureMFA(t, true)
	calls := 0
	f.restorer.replace = func(from, to string) error {
		calls++

		if err := Replace(from, to); err != nil {
			return err
		}

		return f.live.WriteFile("mfa.json", []byte("external edit after first replacement"), 0o600)
	}

	value, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if value == nil || value.Completed || value.Replaced != 1 || err == nil || calls != 1 {
		t.Fatalf("late drift=%+v calls=%d %v", value, calls, err)
	}

	if data, err := f.live.ReadFile("mfa.json"); err != nil || string(data) != "external edit after first replacement" {
		t.Fatalf("overwrote late edit=%q %v", data, err)
	}
}

func TestSnapshotRestoreRejectsCaseAliasesAcrossBackups(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)
	f.restorer.store.selectPath = func(_, path string) SnapshotKind {
		if strings.EqualFold(path, "account.json") || path == "mfa.json" {
			return SnapshotFile
		}

		return SnapshotIgnore
	}

	root, err := os.OpenRoot(f.target.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.Rename("config/account.json", "config/ACCOUNT.json"); err != nil {
		t.Fatal(err)
	}

	f.target.Files[0].Path = "ACCOUNT.json"

	data, err := json.Marshal(f.target.SnapshotManifest)
	if err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile("manifest.json", data, 0o600); err != nil {
		t.Fatal(err)
	}

	if value, err := f.restorer.Restore(t.Context(), f.target, f.safety); err == nil || value != nil && value.Attempted {
		t.Fatalf("case aliases accepted=%+v %v", value, err)
	}

	if data, err := f.live.ReadFile("account.json"); err != nil || string(data) != "new account" {
		t.Fatalf("case collision changed live data=%q %v", data, err)
	}
}

func TestSnapshotRestoreReplacesRemovesAndRetainsBothBackups(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err != nil || result == nil || !result.Completed || !result.Attempted || result.Replaced != 1 || result.Removed != 1 {
		t.Fatalf("restore=%+v error=%v", result, err)
	}

	if data, err := f.live.ReadFile("account.json"); err != nil || string(data) != "old account" {
		t.Fatalf("account=%q %v", data, err)
	}

	if _, err := f.live.Stat("mfa.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new MFA retained: %v", err)
	}

	if data, err := f.live.ReadFile("unselected.txt"); err != nil || string(data) != "untouched" {
		t.Fatalf("outside selection changed=%q %v", data, err)
	}

	for _, id := range []string{f.target.ID, f.safety.ID} {
		if _, err := f.restorer.reader.Open(t.Context(), id); err != nil {
			t.Fatalf("backup lost: %v", err)
		}
	}
}

func TestSnapshotRestoreRefusesDriftTamperAndCancellationWithoutLiveWrites(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"live drift", "backup tamper", "cancel"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newRestoreFileFixture(t)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			expected := []byte("new account")
			if name == "live drift" {
				expected = []byte("bad account")
				if err := f.live.WriteFile("account.json", expected, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if name == "backup tamper" {
				root, err := os.OpenRoot(f.target.Directory)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()

				if err := root.WriteFile("config/account.json", []byte("bad account"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if name == "cancel" {
				cancel()
			}

			result, err := f.restorer.Restore(ctx, f.target, f.safety)
			if err == nil || result != nil && result.Attempted {
				t.Fatalf("accepted %s=%+v %v", name, result, err)
			}

			if data, err := f.live.ReadFile("account.json"); err != nil || !bytes.Equal(data, expected) {
				t.Fatalf("live file changed=%q %v", data, err)
			}
		})
	}
}

func TestSnapshotRestoreRecreatesASelectedMissingFile(t *testing.T) {
	t.Parallel()

	f := newRestoreFileFixture(t)
	if err := f.live.Remove("account.json"); err != nil {
		t.Fatal(err)
	}

	var err error

	f.safety, err = f.restorer.store.Create(t.Context(), json.RawMessage(`{"version":"new"}`))
	if err != nil {
		t.Fatal(err)
	}

	value, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err != nil || value == nil || !value.Completed || value.Replaced != 1 || value.Removed != 1 {
		t.Fatalf("recreate=%+v %v", value, err)
	}

	if data, err := f.live.ReadFile("account.json"); err != nil || string(data) != "old account" {
		t.Fatalf("missing file not recreated=%q %v", data, err)
	}

	assertRestoreCopiesAndCleanup(t, f)
}
