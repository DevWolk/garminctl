package hostfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SnapshotReader inspects a trusted local backup root without restoring files.
type SnapshotReader struct{ directory string }

// NewSnapshotReader performs no IO; the directory comes from local composition.
func NewSnapshotReader(directory string) *SnapshotReader {
	return &SnapshotReader{directory: directory}
}

// List returns manifest metadata only, without verifying retained file bytes.
func (r *SnapshotReader) List(ctx context.Context) ([]Snapshot, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}

	parent, err := r.openParent()
	if errors.Is(err, os.ErrNotExist) {
		return []Snapshot{}, nil
	}

	if err != nil {
		return nil, err
	}

	defer parent.Close()

	file, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	entries, err := file.ReadDir(1001)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	if len(entries) > 1000 {
		return nil, errors.New("snapshot catalog exceeds the directory-entry limit")
	}

	result := make([]Snapshot, 0)

	for _, entry := range entries {
		if !validSnapshotID(entry.Name()) {
			continue
		}

		value, err := r.read(ctx, parent, entry.Name(), false)
		if err != nil {
			return nil, err
		}

		result = append(result, *value)
	}

	slices.SortFunc(result, func(a, b Snapshot) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}

		return strings.Compare(a.ID, b.ID)
	})

	return result, nil
}

// Open checks every retained file against its local manifest, not a publisher key.
func (r *SnapshotReader) Open(ctx context.Context, id string) (*Snapshot, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}

	if !validSnapshotID(id) {
		return nil, errors.New("invalid snapshot identity")
	}

	parent, err := r.openParent()
	if err != nil {
		return nil, err
	}
	defer parent.Close()

	return r.read(ctx, parent, id, true)
}

func (r *SnapshotReader) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if r == nil || !filepath.IsAbs(r.directory) {
		return errors.New("snapshot reader requires a trusted absolute directory")
	}

	return nil
}

func (r *SnapshotReader) openParent() (*os.Root, error) {
	if err := Protect(r.directory, true); err != nil {
		return nil, err
	}

	return os.OpenRoot(r.directory)
}

func (r *SnapshotReader) read(ctx context.Context, parent *os.Root, id string, verify bool) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	directory := filepath.Join(r.directory, id)
	if err := Protect(directory, true); err != nil {
		return nil, err
	}

	root, err := parent.OpenRoot(id)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	manifest, err := readSnapshotManifest(ctx, root, directory, id)
	if err != nil {
		return nil, err
	}

	if verify {
		if err := verifySnapshotLayout(ctx, root, manifest, directory); err != nil {
			return nil, err
		}

		for _, file := range manifest.Files {
			if err := verifyRetainedSnapshotFile(ctx, root, file); err != nil {
				return nil, err
			}
		}
	}

	if err := checkSnapshotDestination(parent, r.directory); err != nil {
		return nil, err
	}

	if err := checkSnapshotDestination(root, directory); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &Snapshot{SnapshotManifest: manifest, Directory: directory}, nil
}

func readSnapshotManifest(ctx context.Context, root *os.Root, directory, id string) (result SnapshotManifest, err error) {
	if err := Protect(filepath.Join(directory, "manifest.json"), false); err != nil {
		return result, err
	}

	info, err := root.Lstat("manifest.json")
	if err != nil {
		return result, err
	}

	if !info.Mode().IsRegular() || info.Size() > snapshotManifestLimit {
		return result, errors.New("invalid snapshot manifest type or size")
	}

	file, err := root.Open("manifest.json")
	if err != nil {
		return result, err
	}

	defer func() { err = errors.Join(err, file.Close()) }()

	data, err := io.ReadAll(snapshotReader{ctx: ctx, reader: io.LimitReader(file, snapshotManifestLimit+1)})
	if err != nil {
		return result, err
	}

	if len(data) > snapshotManifestLimit {
		return result, errors.New("snapshot manifest exceeds the byte limit")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&result); err != nil {
		return result, err
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, errors.New("unexpected trailing snapshot manifest data")
	}

	return result, validateSnapshotManifest(id, result)
}
