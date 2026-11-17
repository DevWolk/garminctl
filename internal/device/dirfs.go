package device

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const timeLayout = "2006-01-02T15:04:05"

// DirFS serves a directory as a device. It is used for mass-storage mounts,
// dumps of a watch and tests. Every access goes through an os.Root, so a
// path can never leave the directory, not through ".." and not through a
// symbolic link.
type DirFS struct {
	root *os.Root
	info Info
}

var _ Device = (*DirFS)(nil)

// NewDirFS returns a device backed by root.
func NewDirFS(root string) (*DirFS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}

	if !st.IsDir() {
		return nil, errors.New("device root is not a directory")
	}

	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}

	info := Info{ID: "dir:" + abs, Name: filepath.Base(abs), Description: "directory " + abs}

	return &DirFS{root: r, info: info}, nil
}

// Info implements Device.
func (d *DirFS) Info() Info { return d.info }

// resolve maps a device path to a path relative to the root, matching names
// case-insensitively like the watch does. When create is set the last
// component may be missing.
func (d *DirFS) resolve(path string, create bool) (string, error) {
	parts := strings.FieldsFunc(strings.ReplaceAll(path, "\\", "/"), func(r rune) bool { return r == '/' })
	cur := "."

	for i, part := range parts {
		if part == "." || part == ".." {
			return "", fmt.Errorf("%w: invalid path", ErrInvalidArgument)
		}

		// Every element goes through the directory listing, even one that
		// exists as typed: on a case-insensitive filesystem (Windows) a
		// direct lookup would succeed without telling the on-disk spelling.
		found, err := d.findFold(cur, part)
		if err != nil {
			return "", d.lookupError(cur, err)
		}

		if found == "" {
			if create && i == len(parts)-1 {
				return filepath.Join(cur, part), nil
			}

			return "", ErrNotFound
		}

		cur = filepath.Join(cur, found)
	}

	return cur, nil
}

// lookupError keeps absence apart from a directory that could not be read:
// only a missing parent, or one that is a file, means the path does not exist.
func (d *DirFS) lookupError(dir string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}

	if st, statErr := d.root.Stat(dir); statErr == nil && !st.IsDir() {
		return ErrNotFound
	}

	return err
}

// findFold returns the entry of dir named name, or the first one that
// matches ignoring case, or "" when there is none.
func (d *DirFS) findFold(dir, name string) (string, error) {
	entries, err := d.readDir(dir)
	if err != nil {
		return "", err
	}

	fold := ""

	for _, e := range entries {
		if e.Name() == name {
			return name, nil
		}

		if fold == "" && strings.EqualFold(e.Name(), name) {
			fold = e.Name()
		}
	}

	return fold, nil
}

func (d *DirFS) readDir(rel string) ([]os.DirEntry, error) {
	f, err := d.root.Open(rel)
	if err != nil {
		return nil, err
	}

	entries, err := f.ReadDir(-1)

	return entries, errors.Join(err, f.Close())
}

// List implements Device.
func (d *DirFS) List(_ context.Context, path string) ([]Entry, error) {
	p, err := d.resolve(path, false)
	if err != nil {
		return nil, err
	}

	entries, err := d.readDir(p)
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(entries))

	for _, e := range entries {
		st, err := e.Info()
		if err != nil {
			continue
		}

		en := Entry{Name: e.Name(), Dir: e.IsDir(), Modified: st.ModTime().UTC().Format(timeLayout)}
		if !e.IsDir() {
			en.Size = st.Size()
		}

		out = append(out, en)
	}

	return out, nil
}

// Stat implements Device.
func (d *DirFS) Stat(_ context.Context, path string) (Entry, error) {
	p, err := d.resolve(path, false)
	if err != nil {
		return Entry{}, err
	}

	st, err := d.root.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, ErrNotFound
	}

	if err != nil {
		return Entry{}, err
	}

	en := Entry{Name: st.Name(), Dir: st.IsDir(), Modified: st.ModTime().UTC().Format(timeLayout)}
	if !st.IsDir() {
		en.Size = st.Size()
	}

	return en, nil
}

// Read implements Device.
func (d *DirFS) Read(_ context.Context, path string) ([]byte, error) {
	p, err := d.resolve(path, false)
	if err != nil {
		return nil, err
	}

	return d.root.ReadFile(p)
}

// Write implements Device.
func (d *DirFS) Write(_ context.Context, path string, data []byte) error {
	p, err := d.resolve(path, true)
	if err != nil {
		return err
	}

	return d.root.WriteFile(p, data, 0o600)
}

// Delete implements Device.
func (d *DirFS) Delete(_ context.Context, path string) error {
	p, err := d.resolve(path, false)
	if err != nil {
		return err
	}

	return d.root.Remove(p)
}

// Mkdir implements Device.
func (d *DirFS) Mkdir(_ context.Context, path string) error {
	p, err := d.resolve(path, true)
	if err != nil {
		return err
	}

	return d.root.MkdirAll(p, 0o750)
}

// Close implements Device.
func (d *DirFS) Close() error { return d.root.Close() }
