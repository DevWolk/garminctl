package device

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// newDump builds a small copy of a watch's storage in a temp dir.
func newDump(t *testing.T) (*DirFS, string) {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "GARMIN", "Settings"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "GARMIN", "Settings", "Settings.fit"), []byte("fit"), 0o600); err != nil {
		t.Fatal(err)
	}

	d, err := NewDirFS(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	return d, root
}

func TestNewDirFS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    func(t *testing.T) string
		wantErr bool
	}{
		{name: "directory", path: func(t *testing.T) string { t.Helper(); return t.TempDir() }},
		{name: "missing", path: func(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "nope") }, wantErr: true},
		{name: "file", path: func(t *testing.T) string {
			t.Helper()

			p := filepath.Join(t.TempDir(), "f")
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}

			return p
		}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, err := NewDirFS(tt.path(t))
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewDirFS error = %v, wantErr %v", err, tt.wantErr)
			}

			if d != nil {
				_ = d.Close()
			}
		})
	}
}

func TestDirFS_ResolvesCaseInsensitively(t *testing.T) {
	t.Parallel()

	d, _ := newDump(t)
	ctx := context.Background()

	for _, p := range []string{"GARMIN/Settings/Settings.fit", "garmin/settings/SETTINGS.FIT", "Garmin\\Settings\\settings.fit"} {
		data, err := d.Read(ctx, p)
		if err != nil || string(data) != "fit" {
			t.Fatalf("Read(%q) = %q, %v", p, data, err)
		}
	}

	en, err := d.Stat(ctx, "garmin/SETTINGS")
	if err != nil || !en.Dir || en.Name != "Settings" {
		t.Fatalf("Stat = %+v, %v", en, err)
	}

	entries, err := d.List(ctx, "")
	if err != nil || len(entries) != 1 || entries[0].Name != "GARMIN" || !entries[0].Dir {
		t.Fatalf("List root = %+v, %v", entries, err)
	}

	if _, err := d.Read(ctx, "GARMIN/Settings/missing.fit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: err = %v, want ErrNotFound", err)
	}
}

func TestDirFS_WritesIntoExistingFolders(t *testing.T) {
	t.Parallel()

	d, root := newDump(t)
	ctx := context.Background()

	if err := d.Write(ctx, "garmin/newfiles/A.FIT", []byte("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("write into a missing folder: err = %v, want ErrNotFound", err)
	}

	if err := d.Mkdir(ctx, "garmin/NewFiles"); err != nil {
		t.Fatal(err)
	}

	if err := d.Write(ctx, "GARMIN/newfiles/A.FIT", []byte("x")); err != nil {
		t.Fatal(err)
	}

	// The file lands in the folder as it was created, not as it was spelled.
	if _, err := os.Stat(filepath.Join(root, "GARMIN", "NewFiles", "A.FIT")); err != nil {
		t.Fatal(err)
	}

	if err := d.Delete(ctx, "garmin/newfiles/a.fit"); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Stat(ctx, "GARMIN/NewFiles/A.FIT"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
}

func TestDirFS_StaysInsideRoot(t *testing.T) {
	t.Parallel()

	d, root := newDump(t)
	ctx := context.Background()

	outside := filepath.Join(filepath.Dir(root), "outside-"+filepath.Base(root))
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Remove(outside) })

	if err := os.Symlink(outside, filepath.Join(root, "GARMIN", "link")); err != nil {
		t.Skip("symlinks not supported here:", err)
	}

	for _, p := range []string{"../" + filepath.Base(outside), "GARMIN/../../x", "GARMIN/link"} {
		if data, err := d.Read(ctx, p); err == nil {
			t.Fatalf("Read(%q) escaped the root: %q", p, data)
		}
	}

	if err := d.Write(ctx, "GARMIN/link", []byte("x")); err == nil {
		t.Fatal("Write through a symlink escaped the root")
	}

	if b, err := os.ReadFile(outside); err != nil || string(b) != "secret" { //nolint:gosec // test file in the temp dir
		t.Fatalf("outside file changed: %q, %v", b, err)
	}
}

func TestDirFS_ReportsAbsenceOnlyForMissingPaths(t *testing.T) {
	t.Parallel()

	d, _ := newDump(t)
	ctx := context.Background()

	for _, missing := range []string{
		"GARMIN/Settings/Missing.fit",
		"GARMIN/Missing/Settings.fit",
		"GARMIN/Settings/Settings.fit/child",
	} {
		if _, err := d.Read(ctx, missing); !errors.Is(err, ErrNotFound) {
			t.Errorf("Read(%q) = %v, want ErrNotFound", missing, err)
		}

		if _, err := d.Stat(ctx, missing); !errors.Is(err, ErrNotFound) {
			t.Errorf("Stat(%q) = %v, want ErrNotFound", missing, err)
		}
	}
}

func TestDirFS_FailedLookupIsNotAbsence(t *testing.T) {
	t.Parallel()

	d, _ := newDump(t)
	ctx := context.Background()

	// A closed root fails every lookup although the file is still there.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Read(ctx, "GARMIN/Settings/Settings.fit"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Read over a failed lookup = %v, want an error other than ErrNotFound", err)
	}

	if _, err := d.Stat(ctx, "GARMIN/Settings/Settings.fit"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Stat over a failed lookup = %v, want an error other than ErrNotFound", err)
	}

	if _, err := d.List(ctx, "GARMIN/Settings"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("List over a failed lookup = %v, want an error other than ErrNotFound", err)
	}
}
