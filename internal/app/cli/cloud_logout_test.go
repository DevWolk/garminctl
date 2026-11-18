package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
)

// accountBackup saves a backup that holds the session, the pending
// verification state and one unrelated file of the given account store.
func accountBackup(t *testing.T, account string) (*hostfile.Snapshot, string) {
	t.Helper()

	config := filepath.Dir(account)

	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string]string{account: `{"email":"reader@example.invalid"}`, account + ".mfa": `{"opaque":"fake-pending"}`, filepath.Join(config, "kept.json"): `{}`} {
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	record := application.PackageRecord{ID: strings.Repeat("a", 64), Tag: "v1.2.0", Digest: strings.Repeat("b", 64), OS: "linux", Arch: "amd64", Installation: "deb", Bytes: 12}

	metadata, err := json.Marshal(struct{ Current, Update application.PackageRecord }{record, record})
	if err != nil {
		t.Fatal(err)
	}

	backups := filepath.Join(config, "application-backups")
	store := hostfile.NewSnapshotStore(backups, []hostfile.SnapshotSource{{Name: "config", Directory: config}}, func(_, path string) hostfile.SnapshotKind {
		if strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".mfa") {
			return hostfile.SnapshotFile
		}

		return hostfile.SnapshotIgnore
	})

	snapshot, err := store.Create(t.Context(), metadata)
	if err != nil || len(snapshot.Files) != 3 {
		t.Fatalf("fixture backup=%+v error=%v", snapshot, err)
	}

	return snapshot, backups
}

func TestCloudLogoutRemovesSessionCopiesFromApplicationBackups(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("APPDATA", directory)

	account := gc.DefaultStorePath()
	snapshot, backups := accountBackup(t, account)

	var out, diagnostics bytes.Buffer
	if err := Run("dev", []string{"cloud", "logout"}, &out, &diagnostics); err != nil || !strings.Contains(out.String(), "signed out") {
		t.Fatalf("logout: %v out=%q diagnostics=%q", err, &out, &diagnostics)
	}

	for _, name := range []string{account, account + ".mfa", filepath.Join(snapshot.Directory, "config", filepath.Base(account)), filepath.Join(snapshot.Directory, "config", filepath.Base(account)+".mfa")} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived sign-out: %v", name, err)
		}
	}

	verified, err := hostfile.NewSnapshotReader(backups).Open(t.Context(), snapshot.ID)
	if err != nil || len(verified.Files) != 1 || verified.Files[0].Path != "kept.json" {
		t.Fatalf("backup after sign-out=%+v error=%v", verified, err)
	}
}
