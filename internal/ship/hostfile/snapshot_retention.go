package hostfile

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// snapshotPendingLifetime is how long an unpublished copy may go unchanged
// before it counts as the leftover of an interrupted backup.
const snapshotPendingLifetime = 24 * time.Hour

const snapshotManifestDraft = "manifest.json.new"

// Remove deletes one snapshot by identity, whether or not its manifest is readable.
func (r *SnapshotReader) Remove(ctx context.Context, id string) error {
	if err := r.check(ctx); err != nil {
		return err
	}

	if !validSnapshotID(id) {
		return errors.New("invalid snapshot identity")
	}

	parent, err := r.openParent()
	if errors.Is(err, os.ErrNotExist) {
		return ErrSnapshotNotRetained
	}

	if err != nil {
		return err
	}

	defer parent.Close()

	if _, err := parent.Lstat(id); errors.Is(err, os.ErrNotExist) {
		return ErrSnapshotNotRetained
	} else if err != nil {
		return err
	}

	return parent.RemoveAll(id)
}

// Prune keeps the newest snapshots, up to keep, by their recorded creation
// time, and clears unpublished copies left by interrupted backups. Snapshots
// this build cannot read are left for Remove.
func (r *SnapshotReader) Prune(ctx context.Context, keep int) error {
	if keep < 1 {
		return errors.New("snapshot retention keeps at least one snapshot")
	}

	rows, _, err := r.Catalog(ctx)
	if err != nil {
		return err
	}

	parent, err := r.openParent()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	defer parent.Close()

	var failed error

	for _, row := range rows[min(keep, len(rows)):] {
		failed = errors.Join(failed, parent.RemoveAll(row.ID))
	}

	entries, err := snapshotEntries(parent)
	if err != nil {
		return errors.Join(failed, err)
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}

		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			failed = errors.Join(failed, err)

			continue
		}

		if time.Since(info.ModTime()) >= snapshotPendingLifetime {
			failed = errors.Join(failed, parent.RemoveAll(entry.Name()))
		}
	}

	return failed
}

// Forget deletes the named retained copies from every snapshot and unpublished
// copy under the root, and drops them from each manifest this build can read,
// so those snapshots stay valid without the files. It continues past an entry
// it cannot change and reports every failure.
func (r *SnapshotReader) Forget(ctx context.Context, area string, paths ...string) error {
	if err := r.check(ctx); err != nil {
		return err
	}

	for _, path := range paths {
		if err := validateSnapshotFileRecord(SnapshotFileInfo{Area: area, Path: path, Digest: strings.Repeat("0", 64)}, nil); err != nil {
			return err
		}
	}

	parent, err := r.openParent()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	defer parent.Close()

	entries, err := snapshotEntries(parent)
	if err != nil {
		return err
	}

	var failed error

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(failed, err)
		}

		if validSnapshotID(entry.Name()) || strings.HasPrefix(entry.Name(), ".pending-") {
			failed = errors.Join(failed, r.forget(ctx, parent, entry.Name(), area, paths))
		}
	}

	return failed
}

func (r *SnapshotReader) forget(ctx context.Context, parent *os.Root, name, area string, paths []string) error {
	root, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}

	defer root.Close()

	// The copies go first: an interruption then leaves a snapshot that fails
	// verification, never a manifest that hides a copy still on disk.
	var failed error

	for _, path := range paths {
		name := filepath.Join(area, filepath.FromSlash(path))
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = errors.Join(failed, err)

			continue
		}

		failed = errors.Join(failed, removeEmptySnapshotDirectories(root, name))
	}

	if failed != nil || !validSnapshotID(name) {
		return failed
	}

	directory := filepath.Join(r.directory, name)

	manifest, err := readSnapshotManifest(ctx, root, directory, name)
	if err != nil {
		// A manifest this build cannot read is left as written.
		return ctx.Err()
	}

	kept := slices.DeleteFunc(slices.Clone(manifest.Files), func(file SnapshotFileInfo) bool {
		return file.Area == area && slices.Contains(paths, file.Path)
	})
	if len(kept) == len(manifest.Files) {
		return nil
	}

	manifest.Files = kept

	if err := removeSnapshotDraft(root); err != nil {
		return err
	}

	if err := writeSnapshotManifestAs(ctx, root, snapshotManifestDraft, manifest, directory); err != nil {
		return errors.Join(err, removeSnapshotDraft(root))
	}

	return Replace(filepath.Join(directory, snapshotManifestDraft), filepath.Join(directory, "manifest.json"))
}

// removeEmptySnapshotDirectories drops the directories a removed file left
// empty: a snapshot verifies only with directories that hold a declared file.
func removeEmptySnapshotDirectories(root *os.Root, name string) error {
	for directory := filepath.Dir(name); directory != "."; directory = filepath.Dir(directory) {
		file, err := root.Open(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return err
		}

		entries, err := file.ReadDir(1)
		_ = file.Close()

		if len(entries) != 0 {
			return nil
		}

		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		if err := root.Remove(directory); err != nil {
			return err
		}
	}

	return nil
}

func removeSnapshotDraft(root *os.Root) error {
	if err := root.Remove(snapshotManifestDraft); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

// snapshotEntries lists the directories under a backup root: published
// snapshots and unpublished copies alike.
func snapshotEntries(parent *os.Root) ([]fs.DirEntry, error) {
	file, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(entries, func(entry fs.DirEntry) bool { return !entry.IsDir() }), nil
}
