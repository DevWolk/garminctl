package hostfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

// SnapshotRestoreResult records live-file attempts without promising an atomic set.
// Cleanup reports a staging directory left behind after every reviewed change
// was applied; it does not make the restore incomplete.
type SnapshotRestoreResult struct {
	Attempted, Completed bool
	Replaced, Removed    int
	Cleanup              error
}

// SnapshotRestorer replaces selected data only through caller-owned stopped-writer admission.
// It never stops a process, starts an installer or removes either backup.
type SnapshotRestorer struct {
	reader  *SnapshotReader
	store   *SnapshotStore
	mu      sync.Mutex
	replace func(string, string) error
}

// NewSnapshotRestorer binds trusted roots and selection without doing IO.
func NewSnapshotRestorer(directory string, sources []SnapshotSource, selectPath func(string, string) SnapshotKind) *SnapshotRestorer {
	return &SnapshotRestorer{reader: NewSnapshotReader(directory), store: NewSnapshotStore(directory, sources, selectPath), replace: Replace}
}

// Restore re-verifies both retained copies and refuses live drift before replacement.
// A failure after Attempted may leave a partial set; callers must keep writers stopped.
func (r *SnapshotRestorer) Restore(ctx context.Context, target, safety *Snapshot) (*SnapshotRestoreResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !r.mu.TryLock() {
		return nil, errors.New("snapshot restore is already running")
	}
	defer r.mu.Unlock()

	if err := r.store.validate(ctx, []byte("{}")); err != nil {
		return nil, err
	}

	old, err := r.verifyExpected(ctx, target)
	if err != nil {
		return nil, err
	}

	newer, err := r.verifyExpected(ctx, safety)
	if err != nil {
		return nil, err
	}

	if err := r.validateSelection(old, newer); err != nil {
		return nil, err
	}

	if err := r.checkLive(ctx, newer.Files); err != nil {
		return nil, err
	}

	staged, err := r.stage(ctx, old, newer)
	if err != nil {
		return nil, err
	}

	if err := r.checkLive(ctx, newer.Files); err != nil {
		return nil, errors.Join(err, staged.close())
	}

	result := new(SnapshotRestoreResult)
	if err := r.apply(ctx, staged, newer, result); err != nil {
		return result, errors.Join(err, staged.close())
	}

	// The staged copies have all moved into place, so a directory that cannot
	// be removed now holds no data and must not fail the verified restore.
	result.Cleanup = staged.close()

	if err := r.checkLive(ctx, old.Files); err != nil {
		return result, err
	}

	result.Completed = true

	return result, nil
}

func (r *SnapshotRestorer) verifyExpected(ctx context.Context, expected *Snapshot) (*Snapshot, error) {
	if expected == nil {
		return nil, errors.New("restore requires both retained snapshots")
	}

	actual, err := r.reader.Open(ctx, expected.ID)
	if err != nil {
		return nil, err
	}

	if !reflect.DeepEqual(actual.SnapshotManifest, expected.SnapshotManifest) {
		return nil, errors.New("retained snapshot changed after review")
	}

	return actual, nil
}

func (r *SnapshotRestorer) validateSelection(target, safety *Snapshot) error {
	if target.ID == safety.ID {
		return errors.New("restore requires a distinct safety snapshot")
	}

	areas := make(map[string]bool)
	for _, source := range r.store.sources {
		areas[source.Name] = true
	}

	paths := make(map[string]string)

	for _, snapshot := range []*Snapshot{target, safety} {
		for _, file := range snapshot.Files {
			if !areas[file.Area] || r.store.selectPath(file.Area, file.Path) != SnapshotFile {
				return errors.New("restore file is outside the selected source roots")
			}

			name := file.Area + "/" + file.Path

			key := strings.ToLower(name)
			if prior, ok := paths[key]; ok && prior != name {
				return errors.New("restore paths differ only by case")
			}

			paths[key] = name
		}
	}

	return nil
}

func (r *SnapshotRestorer) checkLive(ctx context.Context, expected []SnapshotFileInfo) error {
	capture, err := r.store.capture(ctx)
	if err != nil {
		return err
	}
	defer capture.close()

	if len(capture.files) != len(expected) {
		return errors.New("live state no longer matches its safety snapshot")
	}

	files := make(map[string]SnapshotFileInfo, len(expected))
	for _, file := range expected {
		files[file.Area+"/"+file.Path] = file
	}

	for _, file := range capture.files {
		record, ok := files[file.area+"/"+file.path]
		if !ok || record.Bytes != file.info.Size() {
			return errors.New("live state differs from its reviewed snapshot")
		}

		if err := verifySnapshotCopy(ctx, capture.roots[file.source].root, filepath.FromSlash(file.path), record); err != nil {
			return err
		}
	}

	return r.store.verifySource(ctx, capture)
}

func restoreFileMap(files []SnapshotFileInfo) map[string]SnapshotFileInfo {
	result := make(map[string]SnapshotFileInfo, len(files))
	for _, file := range files {
		result[file.Area+"/"+file.Path] = file
	}

	return result
}

func checkRestoreFile(ctx context.Context, root *os.Root, file SnapshotFileInfo, exists bool) error {
	if !exists {
		if _, err := root.Lstat(filepath.FromSlash(file.Path)); errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return errors.New("a restore destination appeared after review")
	}

	return verifyRetainedSnapshotFile(ctx, root, SnapshotFileInfo{Area: "", Path: file.Path, Bytes: file.Bytes, Digest: file.Digest})
}
