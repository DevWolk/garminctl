//go:build !windows

package hostfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotReaderRejectsSymlinksAndKeepsOutsideFiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"directory", "manifest", "data"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reader, snapshot, _ := snapshotReadFixture(t)

			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("unchanged outside"), 0o600); err != nil {
				t.Fatal(err)
			}

			selected := snapshot.Directory
			if name == "manifest" {
				selected = filepath.Join(selected, "manifest.json")
			}

			if name == "data" {
				selected = filepath.Join(selected, "config", "state.json")
			}

			if err := os.RemoveAll(selected); err != nil {
				t.Fatal(err)
			}

			if err := os.Symlink(outside, selected); err != nil {
				t.Fatal(err)
			}

			if value, err := reader.Open(t.Context(), snapshot.ID); value != nil || err == nil {
				t.Fatalf("accepted symlink=%+v %v", value, err)
			}

			outsideRoot, err := os.OpenRoot(filepath.Dir(outside))
			if err != nil {
				t.Fatal(err)
			}
			defer outsideRoot.Close()

			got, err := outsideRoot.ReadFile(filepath.Base(outside))
			if err != nil || string(got) != "unchanged outside" {
				t.Fatalf("outside data changed=%q %v", got, err)
			}
		})
	}
}

func TestSnapshotReaderRestrictsBackupPermissionsWithoutTouchingLiveState(t *testing.T) {
	t.Parallel()
	reader, snapshot, _ := snapshotReadFixture(t)

	paths := []string{reader.directory, snapshot.Directory, filepath.Join(snapshot.Directory, "config"), filepath.Join(snapshot.Directory, "manifest.json"), filepath.Join(snapshot.Directory, "config", "state.json")}
	for _, name := range paths {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}

		mode := os.FileMode(0o644)
		if info.IsDir() {
			mode = 0o755
		}

		if err := os.Chmod(name, mode); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := reader.Open(t.Context(), snapshot.ID); err != nil {
		t.Fatal(err)
	}

	for _, name := range paths {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}

		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}

		if info.Mode().Perm() != want {
			t.Fatalf("permissions %s=%o want=%o", name, info.Mode().Perm(), want)
		}
	}
}
