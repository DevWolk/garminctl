package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/gc"
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
