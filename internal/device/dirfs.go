package device

import (
	"context"
	"errors"
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
			return "", errors.New("invalid path")
		}

		cand := filepath.Join(cur, part)
		if _, err := d.root.Lstat(cand); err == nil {
			cur = cand
			continue
		}

		found, err := d.findFold(cur, part)
		if err != nil {
			return "", ErrNotFound
		}

		if found == "" {
			if create && i == len(parts)-1 {
				return cand, nil
			}

			return "", ErrNotFound
		}

		cur = filepath.Join(cur, found)
	}

	return cur, nil
}

// findFold returns the entry of dir whose name equals name ignoring case,
// or "" when there is none.
func (d *DirFS) findFold(dir, name string) (string, error) {
	entries, err := d.readDir(dir)
	if err != nil {
		return "", err
	}

	for _, e := range entries {
		if strings.EqualFold(e.Name(), name) {
			return e.Name(), nil
		}
	}

	return "", nil
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
	if err != nil {
		return Entry{}, ErrNotFound
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
