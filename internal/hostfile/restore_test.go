package hostfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	restoreConfigArea = "config"
	restoreOldAccount = "old account"
	restoreMFA        = "mfa.json"
	// restoreLedger is selected like the others and absent from the fixture backups.
	restoreLedger = "ledger.json"
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
		if path == "account.json" || path == restoreMFA || path == restoreLedger {
			return SnapshotFile
		}

		return SnapshotIgnore
	}
	store := NewSnapshotStore(backups, []SnapshotSource{{Name: restoreConfigArea, Directory: live}}, selectPath)

	if err := fs.WriteFile("account.json", []byte(restoreOldAccount), 0o600); err != nil {
		t.Fatal(err)
	}

	if oldMFA {
		if err := fs.WriteFile(restoreMFA, []byte("old pending MFA"), 0o600); err != nil {
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

	if err := fs.WriteFile(restoreMFA, []byte("new pending MFA"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fs.WriteFile("unselected.txt", []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}

	safety, err := store.Create(t.Context(), json.RawMessage(`{"version":"new"}`))
	if err != nil {
		t.Fatal(err)
	}

	return restoreFileFixture{restorer: NewSnapshotRestorer(backups, []SnapshotSource{{Name: restoreConfigArea, Directory: live}}, selectPath), target: target, safety: safety, live: fs}
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

	if data, err := f.live.ReadFile("account.json"); err != nil || string(data) != restoreOldAccount {
		t.Fatalf("first effect hidden/undone=%q %v", data, err)
	}

	if data, err := f.live.ReadFile(restoreMFA); err != nil || string(data) != "new pending MFA" {
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

		return f.live.WriteFile(restoreMFA, []byte("external edit after first replacement"), 0o600)
	}

	value, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if value == nil || value.Completed || value.Replaced != 1 || err == nil || calls != 1 {
		t.Fatalf("late drift=%+v calls=%d %v", value, calls, err)
	}

	if data, err := f.live.ReadFile(restoreMFA); err != nil || string(data) != "external edit after first replacement" {
		t.Fatalf("overwrote late edit=%q %v", data, err)
	}
}

func TestSnapshotRestoreRejectsCaseAliasesAcrossBackups(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)
	f.restorer.store.selectPath = func(_, path string) SnapshotKind {
		if strings.EqualFold(path, "account.json") || path == restoreMFA {
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

	if data, err := f.live.ReadFile("account.json"); err != nil || string(data) != restoreOldAccount {
		t.Fatalf("account=%q %v", data, err)
	}

	if _, err := f.live.Stat(restoreMFA); !errors.Is(err, os.ErrNotExist) {
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

	if data, err := f.live.ReadFile("account.json"); err != nil || string(data) != restoreOldAccount {
		t.Fatalf("missing file not recreated=%q %v", data, err)
	}

	assertRestoreCopiesAndCleanup(t, f)
}

const restoreStagingPrefix = ".garminctl-restore-"

// onLiveWalk runs act once, the first time the restorer walks a live path accepted by match.
func (f restoreFileFixture) onLiveWalk(match func(string) bool, act func()) {
	selectPath := f.restorer.store.selectPath

	var once sync.Once

	f.restorer.store.selectPath = func(area, path string) SnapshotKind {
		if match(path) {
			once.Do(act)
		}

		return selectPath(area, path)
	}
}

func (f restoreFileFixture) assertLive(t *testing.T, name, want string) {
	t.Helper()

	if data, err := f.live.ReadFile(name); err != nil || string(data) != want {
		t.Fatalf("live %s=%q %v, want %q", name, data, err, want)
	}
}

func TestSnapshotRestoreRefusesLiveDriftBeforeStagingOlderCopies(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)

	// A selected file that neither backup knows is not covered by any per-file check.
	if err := f.live.WriteFile(restoreLedger, []byte("written after the safety backup"), 0o600); err != nil {
		t.Fatal(err)
	}

	staged := false

	f.onLiveWalk(func(path string) bool { return strings.HasPrefix(path, restoreStagingPrefix) }, func() { staged = true })

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err == nil || result != nil || staged {
		t.Fatalf("drift accepted or older copies staged first: result=%+v staged=%v error=%v", result, staged, err)
	}

	f.assertLive(t, "account.json", "new account")
	f.assertLive(t, restoreLedger, "written after the safety backup")
	assertRestoreCopiesAndCleanup(t, f)
}

func TestSnapshotRestoreRefusesLiveDriftThatAppearsWhileCopiesAreStaged(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)

	f.onLiveWalk(func(path string) bool { return strings.HasPrefix(path, restoreStagingPrefix) }, func() {
		if err := f.live.WriteFile(restoreLedger, []byte("written during staging"), 0o600); err != nil {
			t.Error(err)
		}
	})

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err == nil || result != nil {
		t.Fatalf("drift during staging accepted: result=%+v error=%v", result, err)
	}

	f.assertLive(t, "account.json", "new account")
	f.assertLive(t, restoreMFA, "new pending MFA")
	f.assertLive(t, restoreLedger, "written during staging")
	assertRestoreCopiesAndCleanup(t, f)
}

func TestSnapshotRestoreStopsOnDriftThatFollowsTheFirstReplacement(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, file string
		removed    int
	}{
		// A selected file unknown to both backups is seen only by the final whole-set verification.
		{name: "unknown selected file fails the final verification", file: restoreLedger, removed: 1},
		// The newer-only file is hashed again immediately before its removal.
		{name: "edited newer file is kept instead of removed", file: restoreMFA},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newRestoreFileFixture(t)

			f.restorer.replace = func(from, to string) error {
				if err := Replace(from, to); err != nil {
					return err
				}

				return f.live.WriteFile(test.file, []byte("written during replacement"), 0o600)
			}

			result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
			if err == nil || result == nil || result.Completed || result.Replaced != 1 || result.Removed != test.removed {
				t.Fatalf("drift accepted: result=%+v error=%v", result, err)
			}

			f.assertLive(t, test.file, "written during replacement")
		})
	}
}

func TestSnapshotRestoreRefusesABackupWhoseManifestDiffersFromTheReview(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"target", "safety"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newRestoreFileFixture(t)

			// The retained copies stay consistent on disk; only the reviewed record differs.
			reviewed := map[string]*Snapshot{"target": f.target, "safety": f.safety}
			changed := *reviewed[name]
			changed.Files = slices.Clone(changed.Files)
			changed.Files[0].Digest = strings.Repeat("0", 64)
			reviewed[name] = &changed

			result, err := f.restorer.Restore(t.Context(), reviewed["target"], reviewed["safety"])
			if err == nil || result != nil {
				t.Fatalf("unreviewed %s backup accepted: result=%+v error=%v", name, result, err)
			}

			f.assertLive(t, "account.json", "new account")
			f.assertLive(t, restoreMFA, "new pending MFA")
		})
	}
}

func TestSnapshotRestoreRefusesTheSameBackupAsTargetAndSafety(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)

	result, err := f.restorer.Restore(t.Context(), f.safety, f.safety)
	if err == nil || result != nil {
		t.Fatalf("restore without a distinct safety backup accepted: result=%+v error=%v", result, err)
	}
}

func TestSnapshotRestoreRefusesABackupFileOutsideTheSelection(t *testing.T) {
	t.Parallel()
	f := restoreFileFixtureMFA(t, true)

	if err := f.live.Remove(restoreMFA); err != nil {
		t.Fatal(err)
	}

	// The older backup still holds mfa.json; the current selection no longer does.
	f.restorer.store.selectPath = func(_, path string) SnapshotKind {
		if path == "account.json" {
			return SnapshotFile
		}

		return SnapshotIgnore
	}

	var err error

	f.safety, err = f.restorer.store.Create(t.Context(), json.RawMessage(`{"version":"new"}`))
	if err != nil {
		t.Fatal(err)
	}

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err == nil || result != nil {
		t.Fatalf("unselected backup file accepted: result=%+v error=%v", result, err)
	}

	f.assertLive(t, "account.json", "new account")

	if _, err := f.live.Stat(restoreMFA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore wrote outside the selection: %v", err)
	}
}

func TestSnapshotRestoreKeepsAFileThatAppearedAtARecreatedPath(t *testing.T) {
	t.Parallel()
	f := restoreFileFixtureMFA(t, true)

	if err := f.live.Remove(restoreMFA); err != nil {
		t.Fatal(err)
	}

	var err error

	f.safety, err = f.restorer.store.Create(t.Context(), json.RawMessage(`{"version":"new"}`))
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	f.restorer.replace = func(from, to string) error {
		calls++

		if err := Replace(from, to); err != nil {
			return err
		}

		return f.live.WriteFile(restoreMFA, []byte("appeared after review"), 0o600)
	}

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err == nil || result == nil || result.Completed || result.Replaced != 1 || calls != 1 {
		t.Fatalf("appeared destination replaced: result=%+v calls=%d error=%v", result, calls, err)
	}

	f.assertLive(t, restoreMFA, "appeared after review")
}

func TestSnapshotRestoreRefusesStagedBytesThatDifferFromTheReviewedBackup(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)

	// The first live walk runs after both retained backups were re-verified.
	f.onLiveWalk(func(path string) bool { return path == "unselected.txt" }, func() {
		root, err := os.OpenRoot(f.target.Directory)
		if err != nil {
			t.Error(err)
			return
		}
		defer root.Close()

		if err := root.WriteFile("config/account.json", []byte("bad account"), 0o600); err != nil {
			t.Error(err)
		}
	})

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err == nil || result != nil {
		t.Fatalf("changed backup bytes staged for replacement: result=%+v error=%v", result, err)
	}

	f.assertLive(t, "account.json", "new account")
	f.assertLive(t, restoreMFA, "new pending MFA")
}
