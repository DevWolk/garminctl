//go:build unix

package updatetransport

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateAncestorRefusesForeignOwnersAndOpenDirectories(t *testing.T) {
	t.Parallel()

	const user = 1000

	for _, tc := range []struct {
		name  string
		owner uint32
		mode  fs.FileMode
		valid bool
	}{
		{"own private directory", user, fs.ModeDir | 0o700, true},
		{"root directory", 0, fs.ModeDir | 0o755, true},
		{"group-writable own directory", user, fs.ModeDir | 0o775, true},
		{"sticky shared directory", 0, fs.ModeDir | fs.ModeSticky | 0o777, true},
		{"own link", user, fs.ModeSymlink | 0o777, true},
		{"directory of another user", user + 1, fs.ModeDir | 0o700, false},
		{"link of another user", user + 1, fs.ModeSymlink | 0o777, false},
		{"world-writable directory", user, fs.ModeDir | 0o777, false},
		{"world-writable root directory", 0, fs.ModeDir | 0o757, false},
		{"file", user, 0o600, false},
	} {
		if err := privateAncestor("/fixture", user, tc.owner, tc.mode); (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestPrepareStageRefusesCacheBelowDirectoryOthersCanChange(t *testing.T) {
	t.Parallel()

	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	open := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // The fixture is the unsafe ancestor under test.
		t.Fatal(err)
	}

	cache := filepath.Join(open, "garminctl", "application-installing")

	directory, executable, err := prepareStage(t.Context(), cache, source)
	if err == nil || directory != "" || executable != "" {
		t.Fatalf("stage prepared below a world-writable directory: %s %v", directory, err)
	}

	if entries, err := os.ReadDir(cache); err != nil || len(entries) != 0 {
		t.Fatalf("refused cache received files: %v %v", entries, err)
	}
}

func TestPrepareStageRefusesCacheReachedThroughLinkInOpenDirectory(t *testing.T) {
	t.Parallel()

	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	open, private := filepath.Join(base, "open"), filepath.Join(base, "private")

	for _, path := range []string{open, private} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // The fixture is the unsafe ancestor under test.
		t.Fatal(err)
	}

	if err := os.Symlink(private, filepath.Join(open, "link")); err != nil {
		t.Fatal(err)
	}

	if directory, _, err := prepareStage(t.Context(), filepath.Join(open, "link", "cache"), source); err == nil {
		t.Fatalf("stage prepared through a replaceable link: %s", directory)
	}
}

func TestPrepareStageAcceptsCacheReachedThroughOwnLink(t *testing.T) {
	t.Parallel()

	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "real"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("real", filepath.Join(base, "home")); err != nil {
		t.Fatal(err)
	}

	directory, executable, err := prepareStage(t.Context(), filepath.Join(base, "home", ".cache", "garminctl", "application-staging"), source)
	if err != nil {
		t.Fatalf("linked home refused: %v", err)
	}

	if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() || filepath.Dir(executable) != directory {
		t.Fatalf("cached CLI=%s %v", executable, err)
	}
}
