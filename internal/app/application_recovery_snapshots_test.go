package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
	"github.com/DevWolk/garminctl/internal/ship/paths"
)

const (
	recoveryOldState = `{"opaque":"fake-old-account-secret"}`
	recoveryNewState = `{"opaque":"fake-new-account-secret"}`
)

type recoverySnapshotFixture struct {
	action          *application.RecoverySnapshotsAction
	snapshot        *application.DataSnapshot
	root            *os.Root
	current, update application.PackageRecord
}

func newRecoverySnapshotFixture(t *testing.T) recoverySnapshotFixture {
	t.Helper()
	config, cache := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)

	account := gc.DefaultStorePath()
	if err := os.MkdirAll(filepath.Dir(account), 0o700); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(filepath.Dir(account))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := root.WriteFile(filepath.Base(account), []byte(recoveryOldState), 0o600); err != nil {
		t.Fatal(err)
	}

	current := application.PackageRecord{ID: strings.Repeat("a", 64), Tag: "v1.2.0", Digest: strings.Repeat("b", 64), OS: "linux", Arch: "amd64", Installation: "deb", Bytes: 12}
	update := current
	update.ID = strings.Repeat("c", 64)
	update.Tag = "v1.3.0"

	snapshot, err := (applicationDataSnapshots{}).Create(t.Context(), application.PreflightInfo{Current: current, Update: update})
	if err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(filepath.Base(account), []byte(recoveryNewState), 0o600); err != nil {
		t.Fatal(err)
	}

	action, err := NewApplicationRecoverySnapshots()
	if err != nil {
		t.Fatal(err)
	}

	return recoverySnapshotFixture{action: action, snapshot: snapshot, root: root, current: current, update: update}
}

func TestApplicationRecoverySnapshotUsesRealBackupAndPreservesNewEdits(t *testing.T) { //nolint:paralleltest // The fixture redirects process config/cache environment.
	f := newRecoverySnapshotFixture(t)

	result, err := f.action.Verify(t.Context(), f.snapshot.ID)
	if err != nil || result == nil || result.Current != f.current || result.Update != f.update || len(result.Files) != 1 {
		t.Fatalf("snapshot=%+v error=%v", result, err)
	}

	encoded, err := json.Marshal(result)
	if err != nil || bytes.Contains(encoded, []byte("fake-old-account-secret")) || bytes.Contains(encoded, []byte("fake-new-account-secret")) {
		t.Fatalf("snapshot contents leaked: %s %v", encoded, err)
	}

	live, err := f.root.ReadFile("garmin-connect.json")
	if err != nil || string(live) != recoveryNewState {
		t.Fatalf("live state changed: %s %v", live, err)
	}
}

func TestApplicationRecoverySnapshotRejectsChangedRetainedBytes(t *testing.T) { //nolint:paralleltest // The fixture redirects process config/cache environment.
	f := newRecoverySnapshotFixture(t)

	retained, err := os.OpenRoot(f.snapshot.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()

	if err := retained.WriteFile("config/garmin-connect.json", bytes.Repeat([]byte("x"), len(recoveryOldState)), 0o600); err != nil {
		t.Fatal(err)
	}

	if value, err := f.action.Verify(t.Context(), f.snapshot.ID); value != nil || err == nil {
		t.Fatalf("accepted changed bytes=%+v %v", value, err)
	}

	if live, err := f.root.ReadFile("garmin-connect.json"); err != nil || string(live) != recoveryNewState {
		t.Fatalf("failed verify restored live state: %s %v", live, err)
	}
}

// backupFromALaterBuild records a format component this build does not know,
// as the safety copy of a rollback to this build does.
func backupFromALaterBuild(t *testing.T, f recoverySnapshotFixture) *hostfile.Snapshot {
	t.Helper()

	metadata, err := json.Marshal(struct {
		Current, Update application.PackageRecord
		Formats         map[string]int
	}{f.current, f.update, map[string]int{"Account": 1, "ActivityCache": 1}})
	if err != nil {
		t.Fatal(err)
	}

	backups := filepath.Dir(f.snapshot.Directory)
	store := hostfile.NewSnapshotStore(backups, []hostfile.SnapshotSource{{Name: application.StateAreaConfig, Directory: filepath.Dir(backups)}}, applicationSnapshotPath)

	snapshot, err := store.Create(t.Context(), metadata)
	if err != nil {
		t.Fatal(err)
	}

	return snapshot
}

func TestApplicationRecoverySnapshotsListBackupsBesideOneFromALaterBuild(t *testing.T) { //nolint:paralleltest // The fixture redirects process config/cache environment.
	f := newRecoverySnapshotFixture(t)
	foreign := backupFromALaterBuild(t, f)

	rows, err := f.action.List(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != f.snapshot.ID {
		t.Fatalf("snapshots=%+v error=%v", rows, err)
	}

	rows, unusable, err := f.action.Catalog(t.Context())
	if err != nil || len(rows) != 1 || len(unusable) != 1 || unusable[0].ID != foreign.ID || !strings.Contains(unusable[0].Reason, "ActivityCache") {
		t.Fatalf("snapshots=%+v unusable=%+v error=%v", rows, unusable, err)
	}

	if err := f.action.Remove(t.Context(), foreign.ID); err != nil {
		t.Fatal(err)
	}

	if _, unusable, err := f.action.Catalog(t.Context()); err != nil || len(unusable) != 0 {
		t.Fatalf("after removal unusable=%+v error=%v", unusable, err)
	}
}

func TestApplicationBackupRetentionWaitsForAnAcceptedUpdate(t *testing.T) { //nolint:paralleltest // The fixture redirects process config/cache environment.
	f := newRecoverySnapshotFixture(t)
	update := application.PreflightInfo{Current: f.current, Update: f.update}

	// Refused or cancelled attempts each leave a backup and remove none.
	for range application.RetainedBackups + 2 {
		if _, err := (applicationDataSnapshots{}).Create(t.Context(), update); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := f.action.List(t.Context())
	if err != nil || len(rows) != application.RetainedBackups+3 {
		t.Fatalf("after attempts: %d backups error=%v", len(rows), err)
	}

	pruneApplicationBackups(t.Context())

	rows, err = f.action.List(t.Context())
	if err != nil || len(rows) != application.RetainedBackups {
		t.Fatalf("after an accepted update: %d backups error=%v", len(rows), err)
	}

	for _, row := range rows {
		if row.ID == f.snapshot.ID {
			t.Fatal("the oldest backup outlived the retention count")
		}
	}
}

func TestForgetApplicationSessionCopiesRemovesAccountFilesFromBackups(t *testing.T) { //nolint:paralleltest // The fixture redirects process config/cache environment.
	f := newRecoverySnapshotFixture(t)

	if err := f.root.WriteFile("garmin-connect.json.mfa", []byte(`{"opaque":"fake-pending-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	writeApplicationState(t, paths.Cache(), "uploaded-123.json", []byte(`{}`))

	second, err := (applicationDataSnapshots{}).Create(t.Context(), application.PreflightInfo{Current: f.current, Update: f.update})
	if err != nil || second.Files != 3 {
		t.Fatalf("fixture backup=%+v error=%v", second, err)
	}

	if err := ForgetApplicationSessionCopies(t.Context()); err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(filepath.Dir(f.snapshot.Directory), func(path string, _ os.DirEntry, err error) error {
		if strings.HasPrefix(filepath.Base(path), "garmin-connect.json") {
			t.Errorf("session copy stayed at %s", path)
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	verified, err := f.action.Verify(t.Context(), second.ID)
	if err != nil || len(verified.Files) != 1 || verified.Files[0].Path != "uploaded-123.json" {
		t.Fatalf("backup after sign-out=%+v error=%v", verified, err)
	}

	if live, err := f.root.ReadFile("garmin-connect.json"); err != nil || string(live) != recoveryNewState {
		t.Fatalf("live account file changed: %s %v", live, err)
	}
}
