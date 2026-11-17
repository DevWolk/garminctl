package hostfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// unreadableSnapshots adds a directory without a manifest and a manifest with a
// field this build does not know beside one readable snapshot.
func unreadableSnapshots(t *testing.T) (*SnapshotReader, *Snapshot) {
	t.Helper()

	reader, snapshot, _ := snapshotReadFixture(t)
	backups := filepath.Dir(snapshot.Directory)

	if err := os.Mkdir(filepath.Join(backups, strings.Repeat("ab", 32)), 0o700); err != nil {
		t.Fatal(err)
	}

	later := filepath.Join(backups, strings.Repeat("cd", 32))
	if err := os.Mkdir(later, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(later, "manifest.json"), []byte(`{"version":1,"id":"`+strings.Repeat("cd", 32)+`","createdAt":"2026-11-17T10:00:00Z","metadata":{},"missingSources":[],"files":[],"compression":"none"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	return reader, snapshot
}

func TestSnapshotReaderListsReadableSnapshotsBesideUnreadableOnes(t *testing.T) {
	t.Parallel()

	reader, snapshot := unreadableSnapshots(t)

	rows, err := reader.List(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != snapshot.ID {
		t.Fatalf("rows=%+v error=%v", rows, err)
	}

	rows, skipped, err := reader.Catalog(t.Context())
	if err != nil || len(rows) != 1 || len(skipped) != 2 || skipped[0].ID != strings.Repeat("ab", 32) || skipped[1].ID != strings.Repeat("cd", 32) || skipped[0].Reason == "" || !strings.Contains(skipped[1].Reason, "compression") {
		t.Fatalf("rows=%+v skipped=%+v error=%v", rows, skipped, err)
	}
}

func TestSnapshotReaderRemoveDeletesUnreadableSnapshotsByIdentity(t *testing.T) {
	t.Parallel()

	reader, snapshot := unreadableSnapshots(t)

	for _, id := range []string{strings.Repeat("ab", 32), strings.Repeat("cd", 32)} {
		if err := reader.Remove(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}

	if err := reader.Remove(t.Context(), strings.Repeat("ab", 32)); !errors.Is(err, ErrSnapshotNotRetained) {
		t.Fatalf("second removal error=%v", err)
	}

	if err := reader.Remove(t.Context(), "../"+snapshot.ID); err == nil {
		t.Fatal("removal accepted a path")
	}

	if rows, skipped, err := reader.Catalog(t.Context()); err != nil || len(rows) != 1 || len(skipped) != 0 {
		t.Fatalf("after removal: rows=%+v skipped=%+v error=%v", rows, skipped, err)
	}
}

func TestSnapshotReaderOpenOfAnUnknownSnapshotSaysItIsNotRetained(t *testing.T) {
	t.Parallel()

	reader, _, _ := snapshotReadFixture(t)
	empty := NewSnapshotReader(filepath.Join(t.TempDir(), "never-created"))

	for name, r := range map[string]*SnapshotReader{"other id": reader, "no backup directory": empty} {
		if result, err := r.Open(t.Context(), strings.Repeat("ef", 32)); result != nil || !errors.Is(err, ErrSnapshotNotRetained) {
			t.Fatalf("%s: result=%+v error=%v", name, result, err)
		}
	}
}

// accountSnapshots keeps a session file, its pending verification state and one
// unrelated file, the way application backups hold account copies.
func accountSnapshots(t *testing.T) (*SnapshotStore, *SnapshotReader, string) {
	t.Helper()

	root := t.TempDir()
	live, backups := filepath.Join(root, "live"), filepath.Join(root, "backups")

	if err := os.Mkdir(live, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"session.json", "session.json.mfa", "ledger.json"} {
		if err := os.WriteFile(filepath.Join(live, name), []byte("synthetic "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	store := NewSnapshotStore(backups, []SnapshotSource{{Name: "config", Directory: live}}, func(_, path string) SnapshotKind {
		if strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".mfa") {
			return SnapshotFile
		}

		return SnapshotIgnore
	})

	return store, NewSnapshotReader(backups), backups
}

func TestSnapshotReaderForgetRemovesNamedCopiesAndKeepsSnapshotsValid(t *testing.T) {
	t.Parallel()

	store, reader, backups := accountSnapshots(t)

	var ids []string

	for range 2 {
		snapshot, err := store.Create(t.Context(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, snapshot.ID)
	}

	// An interrupted backup and a snapshot written by a build with another manifest.
	pending := filepath.Join(backups, ".pending-"+strings.Repeat("12", 32), "config")
	foreign := filepath.Join(backups, strings.Repeat("ab", 32), "config")

	for _, directory := range []string{pending, foreign} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(directory, "session.json"), []byte("synthetic session"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := reader.Forget(t.Context(), "config", "session.json", "session.json.mfa"); err != nil {
		t.Fatal(err)
	}

	err := filepath.WalkDir(backups, func(path string, _ os.DirEntry, err error) error {
		if strings.HasPrefix(filepath.Base(path), "session.json") {
			t.Errorf("session copy stayed at %s", path)
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range ids {
		verified, err := reader.Open(t.Context(), id)
		if err != nil || len(verified.Files) != 1 || verified.Files[0].Path != "ledger.json" {
			t.Fatalf("snapshot after forgetting: %+v error=%v", verified, err)
		}
	}

	// Nothing is left to forget; a second sign-out changes nothing and succeeds.
	if err := reader.Forget(t.Context(), "config", "session.json", "session.json.mfa"); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotReaderForgetLeavesNoEmptyDirectoryAndRejectsOutsidePaths(t *testing.T) {
	t.Parallel()

	store, reader, _ := accountSnapshots(t)

	snapshot, err := store.Create(t.Context(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	if err := reader.Forget(t.Context(), "config", "session.json", "session.json.mfa", "ledger.json"); err != nil {
		t.Fatal(err)
	}

	if verified, err := reader.Open(t.Context(), snapshot.ID); err != nil || len(verified.Files) != 0 {
		t.Fatalf("emptied snapshot: %+v error=%v", verified, err)
	}

	if err := reader.Forget(t.Context(), "config", "../session.json"); err == nil {
		t.Fatal("accepted a path outside the snapshot")
	}

	if err := NewSnapshotReader(filepath.Join(t.TempDir(), "never-created")).Forget(t.Context(), "config", "session.json"); err != nil {
		t.Fatalf("no backups: %v", err)
	}
}

func TestSnapshotReaderPruneKeepsTheNewestSnapshots(t *testing.T) {
	t.Parallel()

	store, reader, backups := accountSnapshots(t)

	var ids []string

	for range 4 {
		snapshot, err := store.Create(t.Context(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, snapshot.ID)

		time.Sleep(2 * time.Millisecond) // distinct recorded creation times
	}

	unreadable := strings.Repeat("ab", 32)
	abandoned, active := ".pending-"+strings.Repeat("12", 32), ".pending-"+strings.Repeat("34", 32)

	for name, age := range map[string]time.Duration{unreadable: 72 * time.Hour, abandoned: 72 * time.Hour, active: time.Minute} {
		if err := os.Mkdir(filepath.Join(backups, name), 0o700); err != nil {
			t.Fatal(err)
		}

		when := time.Now().Add(-age)
		if err := os.Chtimes(filepath.Join(backups, name), when, when); err != nil {
			t.Fatal(err)
		}
	}

	if err := reader.Prune(t.Context(), 0); err == nil {
		t.Fatal("accepted a policy that keeps nothing")
	}

	if err := reader.Prune(t.Context(), 2); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	want := []string{ids[2], ids[3], unreadable, active}

	slices.Sort(names)
	slices.Sort(want)

	if !slices.Equal(names, want) {
		t.Fatalf("kept %v, want %v", names, want)
	}

	if err := NewSnapshotReader(filepath.Join(t.TempDir(), "never-created")).Prune(t.Context(), 2); err != nil {
		t.Fatalf("no backups: %v", err)
	}
}
