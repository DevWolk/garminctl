package device

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DirFS serves a directory as a device. It is used for mass-storage mounts,
// dumps of a watch and tests.
type DirFS struct {
	root string
	info Info
}

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
	return &DirFS{root: abs, info: Info{ID: "dir:" + abs, Name: filepath.Base(abs), Description: "directory " + abs}}, nil
}

func (d *DirFS) Info() Info { return d.info }

// resolve maps a device path to a host path, matching names
// case-insensitively. When create is set the last component may be missing.
func (d *DirFS) resolve(path string, create bool) (string, error) {
	parts := strings.FieldsFunc(strings.ReplaceAll(path, "\\", "/"), func(r rune) bool { return r == '/' })
	cur := d.root
	for i, part := range parts {
		if part == "." || part == ".." {
			return "", errors.New("invalid path")
		}
		cand := filepath.Join(cur, part)
		if _, err := os.Lstat(cand); err == nil {
			cur = cand
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return "", ErrNotFound
		}
		found := ""
		for _, e := range entries {
			if strings.EqualFold(e.Name(), part) {
				found = e.Name()
				break
			}
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

func (d *DirFS) List(_ context.Context, path string) ([]Entry, error) {
	p, err := d.resolve(path, false)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		st, err := e.Info()
		if err != nil {
			continue
		}
		en := Entry{Name: e.Name(), Dir: e.IsDir(), Modified: st.ModTime().UTC().Format("2006-01-02T15:04:05")}
		if !e.IsDir() {
			en.Size = st.Size()
		}
		out = append(out, en)
	}
	return out, nil
}

func (d *DirFS) Stat(_ context.Context, path string) (Entry, error) {
	p, err := d.resolve(path, false)
	if err != nil {
		return Entry{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return Entry{}, ErrNotFound
	}
	en := Entry{Name: st.Name(), Dir: st.IsDir(), Modified: st.ModTime().UTC().Format("2006-01-02T15:04:05")}
	if !st.IsDir() {
		en.Size = st.Size()
	}
	return en, nil
}

func (d *DirFS) Read(_ context.Context, path string) ([]byte, error) {
	p, err := d.resolve(path, false)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (d *DirFS) Write(_ context.Context, path string, data []byte) error {
	p, err := d.resolve(path, true)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (d *DirFS) Delete(_ context.Context, path string) error {
	p, err := d.resolve(path, false)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func (d *DirFS) Mkdir(_ context.Context, path string) error {
	p, err := d.resolve(path, true)
	if err != nil {
		return err
	}
	return os.MkdirAll(p, 0o755)
}

func (d *DirFS) Close() error { return nil }
