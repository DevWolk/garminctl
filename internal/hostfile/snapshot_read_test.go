package hostfile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snapshotReadFixture(t *testing.T) (*SnapshotReader, *Snapshot, string) {
	t.Helper()
	root := t.TempDir()

	source := filepath.Join(root, "live")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(source, "state.json"), []byte(`{"opaque":"fake-original"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	directory := filepath.Join(root, "backups")
	store := NewSnapshotStore(directory, []SnapshotSource{{Name: "config", Directory: source}}, func(_, path string) SnapshotKind {
		if path == "state.json" {
			return SnapshotFile
		}

		return SnapshotIgnore
	})

	snapshot, err := store.Create(t.Context(), json.RawMessage(`{"note":"fixture"}`))
	if err != nil {
		t.Fatal(err)
	}

	return NewSnapshotReader(directory), snapshot, source
}

func TestSnapshotReaderMetadataAndVerifiedBytesSurviveReopen(t *testing.T) {
	t.Parallel()
	reader, snapshot, live := snapshotReadFixture(t)

	rows, err := reader.List(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != snapshot.ID {
		t.Fatalf("metadata=%+v error=%v", rows, err)
	}
	// An edit made after the backup is independent of inspecting retained bytes.
	current := []byte(`{"opaque":"fake-newer-edit"}`)
	if err := os.WriteFile(filepath.Join(live, "state.json"), current, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := NewSnapshotReader(filepath.Dir(snapshot.Directory)).Open(t.Context(), snapshot.ID)
	if err != nil || result == nil || result.ID != snapshot.ID || len(result.Files) != 1 || result.Files[0] != snapshot.Files[0] {
		t.Fatalf("snapshot=%+v error=%v", result, err)
	}

	liveRoot, err := os.OpenRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	defer liveRoot.Close()

	got, err := liveRoot.ReadFile("state.json")
	if err != nil || string(got) != string(current) {
		t.Fatalf("inspection changed live data=%s error=%v", got, err)
	}
}

func TestSnapshotReaderRejectsTamperedMissingAndAdditionalFiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"tamper", "missing", "additional", "duplicate", "trailing JSON", "unknown version", "foreign id"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reader, snapshot, _ := snapshotReadFixture(t)

			root, err := os.OpenRoot(snapshot.Directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			switch name {
			case "tamper":
				err = root.WriteFile("config/state.json", []byte(`{"opaque":"fake-modified"}`), 0o600)
			case "missing":
				err = root.Remove("config/state.json")
			case "additional":
				err = root.WriteFile("config/extra.json", []byte("unknown"), 0o600)
			default:
				manifest := snapshot.SnapshotManifest
				if name == "duplicate" {
					manifest.Files = append(manifest.Files, manifest.Files[0])
				}

				if name == "unknown version" {
					manifest.Version = 99
				}

				if name == "foreign id" {
					manifest.ID = "not-this-backup"
				}

				data, encodeErr := json.Marshal(manifest)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}

				if name == "trailing JSON" {
					data = append(data, []byte(" {}")...)
				}

				err = root.WriteFile("manifest.json", data, 0o600)
			}

			if err != nil {
				t.Fatal(err)
			}

			if value, err := reader.Open(t.Context(), snapshot.ID); value != nil || err == nil {
				t.Fatalf("accepted %s: %+v error=%v", name, value, err)
			}
		})
	}
}

func TestSnapshotReaderRejectsTraversalCancellationAndNoConfiguration(t *testing.T) {
	t.Parallel()

	reader, snapshot, _ := snapshotReadFixture(t)
	for _, id := range []string{"../outside", "", snapshot.ID + "/manifest.json"} {
		if value, err := reader.Open(t.Context(), id); value != nil || err == nil {
			t.Fatalf("accepted id %q: %+v %v", id, value, err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if value, err := reader.Open(ctx, snapshot.ID); value != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open=%+v %v", value, err)
	}

	if value, err := reader.List(ctx); value != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list=%+v %v", value, err)
	}

	if value, err := NewSnapshotReader("").List(t.Context()); value != nil || err == nil {
		t.Fatalf("unconfigured reader=%+v %v", value, err)
	}
}

func TestSnapshotReaderMissingRootReturnsEmptyAndSkipsPending(t *testing.T) {
	t.Parallel()

	reader, snapshot, _ := snapshotReadFixture(t)
	if err := os.Mkdir(filepath.Join(reader.directory, ".pending-abandoned"), 0o700); err != nil {
		t.Fatal(err)
	}

	rows, err := reader.List(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != snapshot.ID {
		t.Fatalf("pending listing=%+v %v", rows, err)
	}

	rows, err = NewSnapshotReader(filepath.Join(t.TempDir(), "absent")).List(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing listing=%+v %v", rows, err)
	}
}

func TestSnapshotReaderRejectsManifestTraversalAndOversize(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"traversal path", "absolute path", "manifest size", "invalid digest", "unknown field"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reader, snapshot, _ := snapshotReadFixture(t)

			root, err := os.OpenRoot(snapshot.Directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			manifest := snapshot.SnapshotManifest
			if name == "traversal path" {
				manifest.Files[0].Path = "../outside"
			}

			if name == "absolute path" {
				manifest.Files[0].Path = "/outside"
			}

			if name == "invalid digest" {
				manifest.Files[0].Digest = "not-a-digest"
			}

			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}

			if name == "manifest size" {
				data = append(data, []byte(strings.Repeat(" ", snapshotManifestLimit))...)
			}

			if name == "unknown field" {
				data = append([]byte(`{"unknown":true,`), data[1:]...)
			}

			if err := root.WriteFile("manifest.json", data, 0o600); err != nil {
				t.Fatal(err)
			}

			if value, err := reader.Open(t.Context(), snapshot.ID); value != nil || err == nil {
				t.Fatalf("accepted %s=%+v %v", name, value, err)
			}
		})
	}
}
