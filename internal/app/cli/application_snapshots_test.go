package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/hostfile"
)

func TestRecoverySnapshotDispatchUsesNoKeyWatchOrNativeHost(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("APPDATA", directory)

	config := filepath.Join(directory, "garminctl")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(config)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.WriteFile("garmin-connect.json", []byte(`{"secret":"fake-cli-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	record := application.PackageRecord{ID: strings.Repeat("a", 64), Tag: "v1.2.0", Digest: strings.Repeat("b", 64), OS: "linux", Arch: "amd64", Installation: "deb", Bytes: 12}

	metadata, err := json.Marshal(struct{ Current, Update application.PackageRecord }{record, record})
	if err != nil {
		t.Fatal(err)
	}

	store := hostfile.NewSnapshotStore(filepath.Join(config, "application-backups"), []hostfile.SnapshotSource{{Name: "config", Directory: config}}, func(_, path string) hostfile.SnapshotKind {
		if path == "garmin-connect.json" {
			return hostfile.SnapshotFile
		}

		return hostfile.SnapshotIgnore
	})

	snapshot, err := store.Create(t.Context(), metadata)
	if err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"snapshots", "inspect"} {
		args := []string{"-json", rootFlag, "/unavailable-watch", applicationCommandName, "recovery", command}
		if command == "inspect" {
			args = append(args, snapshot.ID)
		}

		var out, diagnostics bytes.Buffer
		if err := Run("dev", args, &out, &diagnostics); err != nil {
			t.Fatalf("%s: %v %s", command, err, &diagnostics)
		}

		if !json.Valid(out.Bytes()) || bytes.Contains(out.Bytes(), []byte("fake-cli-secret")) {
			t.Fatalf("unsafe %s output=%s", command, &out)
		}

		if command == "inspect" && !bytes.Contains(out.Bytes(), []byte(`"verification": "local_manifest"`)) {
			t.Fatalf("inspection claim=%s", &out)
		}
	}
}
