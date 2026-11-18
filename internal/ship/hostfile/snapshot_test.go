package hostfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	snapshotFixtureFile = "state.json"
	snapshotCancelCase  = "cancel copy"
)

func snapshotFixture(t *testing.T) (*SnapshotStore, string, string) {
	t.Helper()
	source, destination := t.TempDir(), filepath.Join(t.TempDir(), "snapshots")
	store := NewSnapshotStore(destination, []SnapshotSource{{Name: "state", Directory: source}}, func(_, rel string) SnapshotKind {
		if rel == snapshotFixtureFile {
			return SnapshotFile
		}

		return SnapshotIgnore
	})

	return store, source, destination
}

func writeSnapshotFixture(t *testing.T, path string, data []byte) {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.WriteFile(filepath.Base(path), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSnapshotFixture(t *testing.T, path string) []byte {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func createSnapshotFixture(t *testing.T, store *SnapshotStore, metadata json.RawMessage) *Snapshot {
	t.Helper()

	receipt, err := store.Create(t.Context(), metadata)
	if err != nil {
		t.Fatal(err)
	}

	return receipt
}

func TestSnapshotPublishesOpaqueVerifiedPrivateCopy(t *testing.T) {
	t.Parallel()

	store, source, destination := snapshotFixture(t)

	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(destination, 0o777); err != nil { //nolint:gosec // Fixture proves the store repairs a pre-existing public output directory.
			t.Fatal(err)
		}
	}

	data := []byte("{\"unknown\":null,\"token\":\"fixture only\"}\n\x00\xff")
	writeSnapshotFixture(t, filepath.Join(source, snapshotFixtureFile), data)
	writeSnapshotFixture(t, filepath.Join(source, "ignored"), []byte("not selected"))

	metadata := json.RawMessage(`{"from":"v1","to":"v2"}`)
	first := createSnapshotFixture(t, store, metadata)

	second := createSnapshotFixture(t, store, metadata)
	if first.ID == second.ID || len(first.ID) != 64 {
		t.Fatal("snapshot overwrote a previous copy")
	}

	for _, receipt := range []*Snapshot{first, second} {
		assertSnapshotFixture(t, receipt, data, metadata)
	}

	if original := readSnapshotFixture(t, filepath.Join(source, snapshotFixtureFile)); !bytes.Equal(original, data) {
		t.Fatal("source changed")
	}

	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 2 {
		t.Fatalf("partial/extra publication=%v %v", entries, err)
	}
}

func assertSnapshotFixture(t *testing.T, receipt *Snapshot, data, metadata []byte) {
	t.Helper()

	base := receipt.Directory

	got := readSnapshotFixture(t, filepath.Join(base, "state", snapshotFixtureFile))
	if !bytes.Equal(got, data) {
		t.Fatal("opaque bytes changed")
	}

	manifestBytes := readSnapshotFixture(t, filepath.Join(base, "manifest.json"))

	var manifest SnapshotManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(data)
	if manifest.Version != 1 || manifest.ID != receipt.ID || len(manifest.Files) != 1 || manifest.Files[0].Digest != hex.EncodeToString(digest[:]) || manifest.Files[0].Bytes != int64(len(data)) || !bytes.Equal(manifest.Metadata, metadata) {
		t.Fatalf("manifest=%+v", manifest)
	}

	assertSnapshotPermissions(t, []string{filepath.Dir(base), base, filepath.Join(base, "state"), filepath.Join(base, "manifest.json"), filepath.Join(base, "state", snapshotFixtureFile)})
}

func assertSnapshotPermissions(t *testing.T, paths []string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		return
	} // DACL execution is a separate native gate.

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}

		if info.Mode().Perm() != want {
			t.Fatalf("permissions=%v want=%v", info.Mode(), want)
		}
	}
}

func TestSnapshotMissingRootsAndEmptyState(t *testing.T) {
	t.Parallel()

	store, source, _ := snapshotFixture(t)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	receipt := createSnapshotFixture(t, store, json.RawMessage(`{}`))
	if len(receipt.Files) != 0 {
		t.Fatalf("logged-out state=%v", receipt)
	}

	data := readSnapshotFixture(t, filepath.Join(receipt.Directory, "manifest.json"))
	if !strings.Contains(string(data), `"missingSources":["state"]`) {
		t.Fatalf("missing root not recorded: %s", data)
	}
}

func TestSnapshotFailureKeepsPreviousAndRemovesOnlyOwnedPending(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"source changes", "file disappears", snapshotCancelCase, "selected directory", "selected symlink", "oversize", "destination file"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store, source, destination := snapshotFixture(t)
			writeSnapshotFixture(t, filepath.Join(source, snapshotFixtureFile), []byte("before"))
			previous := createSnapshotFixture(t, store, json.RawMessage(`{}`))

			stale := filepath.Join(destination, ".pending-unrelated")
			if err := os.Mkdir(stale, 0o700); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			breakSnapshotFixture(t, name, store, source, cancel)

			result, err := store.Create(ctx, json.RawMessage(`{}`))
			if result != nil || err == nil {
				t.Fatalf("unsafe snapshot accepted: %v %v", result, err)
			}

			if name == snapshotCancelCase && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel lost: %v", err)
			}

			assertSnapshotFailureClean(t, destination, previous.ID)
		})
	}
}

func breakSnapshotFixture(t *testing.T, name string, store *SnapshotStore, source string, cancel context.CancelFunc) {
	t.Helper()

	path := filepath.Join(source, snapshotFixtureFile)

	switch name {
	case "source changes", "file disappears", snapshotCancelCase:
		calls := 0
		store.selectPath = func(_, rel string) SnapshotKind {
			if rel != snapshotFixtureFile {
				return SnapshotIgnore
			}

			calls++
			if calls == 2 {
				switch name {
				case "source changes":
					writeSnapshotFixture(t, path, []byte("after and longer"))
				case "file disappears":
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
				case snapshotCancelCase:
					cancel()
				}
			}

			return SnapshotFile
		}
	case "selected directory", "selected symlink":
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}

		if name == "selected directory" {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		} else {
			snapshotSymlinkFixture(t, filepath.Join(t.TempDir(), "outside"), path)
		}
	case "oversize":
		oversizeSnapshotFixture(t, source)
	case "destination file":
		store.destination = filepath.Join(store.destination, "not-a-directory")
		writeSnapshotFixture(t, store.destination, []byte("keep"))
	}
}

func snapshotSymlinkFixture(t *testing.T, target, path string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires a native privilege")
	}

	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func assertSnapshotFailureClean(t *testing.T, destination, previous string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(destination, previous, "manifest.json")); err != nil {
		t.Fatal("previous snapshot removed")
	}

	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") && entry.Name() != ".pending-unrelated" {
			t.Fatal("partial copy leaked")
		}
	}
}

func TestSnapshotRejectsInvalidConfigurationBeforeCreatingOutput(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"relative output", "same root", "duplicate area", "escaping area", "bad metadata", "metadata limit", "already cancelled"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store, source, destination := snapshotFixture(t)
			metadata := json.RawMessage(`{}`)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			switch name {
			case "relative output":
				store.destination = "relative-output"
			case "same root":
				store.destination = source
			case "duplicate area":
				store.sources = append(store.sources, store.sources[0])
			case "escaping area":
				store.sources[0].Name = "../outside"
			case "bad metadata":
				metadata = json.RawMessage(`{`)
			case "metadata limit":
				metadata = json.RawMessage(`"` + strings.Repeat("a", snapshotMetadataLimit) + `"`)
			case "already cancelled":
				cancel()
			}

			if result, err := store.Create(ctx, metadata); result != nil || err == nil {
				t.Fatalf("configuration accepted: %v %v", result, err)
			}

			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid request created output")
			}
		})
	}
}

func TestSnapshotReadbackDetectsCorruption(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	original := []byte("unchanged")
	digest := sha256.Sum256(original)
	expected := SnapshotFileInfo{Bytes: int64(len(original)), Digest: hex.EncodeToString(digest[:])}

	if err := root.WriteFile(snapshotFixtureFile, []byte("corrupted"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := verifySnapshotCopy(t.Context(), root, snapshotFixtureFile, expected); err == nil {
		t.Fatal("corrupt readback accepted")
	}
}

func oversizeSnapshotFixture(t *testing.T, source string) {
	t.Helper()

	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	f, err := root.OpenFile(snapshotFixtureFile, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.Truncate(snapshotFileLimit + 1); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRejectsSourceAndOutputSymlinks(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"source root", "output root"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store, source, _ := snapshotFixture(t)
			target := t.TempDir()

			if name == "source root" {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}

				snapshotSymlinkFixture(t, target, source)
			} else {
				snapshotSymlinkFixture(t, target, store.destination)
			}

			if result, err := store.Create(t.Context(), json.RawMessage(`{}`)); result != nil || err == nil {
				t.Fatalf("symlink accepted: %v %v", result, err)
			}

			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 0 {
				t.Fatal("symlink target changed")
			}
		})
	}
}

func TestSnapshotCaptureRejectsTotalAndEntryLimits(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"bytes", "entries"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store, source, _ := snapshotFixture(t)
			writeSnapshotFixture(t, filepath.Join(source, snapshotFixtureFile), []byte("fixture"))

			value, err := openSnapshotRoot(store.sources[0])
			if err != nil {
				t.Fatal(err)
			}
			defer value.root.Close()

			capture := &snapshotCapture{roots: []snapshotRoot{value}}
			if name == "entries" {
				capture.entries = snapshotEntryLimit
			} else {
				capture.bytes = snapshotTotalLimit
			}

			if err := store.captureEntry(capture, 0, snapshotFixtureFile); err == nil {
				t.Fatal("limit exceeded without error")
			}
		})
	}
}

func TestSnapshotOutputInsideMissingSourceIsValid(t *testing.T) {
	t.Parallel()

	store, source, _ := snapshotFixture(t)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	store.destination = filepath.Join(source, "snapshots")

	result := createSnapshotFixture(t, store, json.RawMessage(`{}`))
	if len(result.Files) != 0 {
		t.Fatal("new source contains selected data")
	}
}

func TestSnapshotRefusesPublicationCollision(t *testing.T) {
	t.Parallel()
	store, source, destination := snapshotFixture(t)
	writeSnapshotFixture(t, filepath.Join(source, snapshotFixtureFile), []byte("fixture"))

	calls := 0

	var collision string

	store.selectPath = func(_, rel string) SnapshotKind {
		if rel != snapshotFixtureFile {
			return SnapshotIgnore
		}

		calls++
		if calls == 2 {
			entries, err := os.ReadDir(destination)
			if err != nil {
				t.Fatal(err)
			}

			for _, entry := range entries {
				if id, ok := strings.CutPrefix(entry.Name(), ".pending-"); ok {
					collision = filepath.Join(destination, id)
					writeSnapshotFixture(t, collision, []byte("unrelated collision"))
				}
			}
		}

		return SnapshotFile
	}
	if result, err := store.Create(t.Context(), json.RawMessage(`{}`)); result != nil || err == nil {
		t.Fatalf("collision replaced: %v %v", result, err)
	}

	if got := readSnapshotFixture(t, collision); string(got) != "unrelated collision" {
		t.Fatal("collision was overwritten")
	}

	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 1 {
		t.Fatalf("pending cleanup=%v %v", entries, err)
	}
}
