package hostfile

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type snapshotRoot struct {
	source SnapshotSource
	root   *os.Root
	info   os.FileInfo
}

type snapshotInput struct {
	source     int
	area, path string
	info       os.FileInfo
}

type snapshotCapture struct {
	roots   []snapshotRoot
	files   []snapshotInput
	entries int
	bytes   int64
}

func (c *snapshotCapture) close() {
	for _, source := range c.roots {
		if source.root != nil {
			_ = source.root.Close()
		}
	}
}

func (c *snapshotCapture) missing() []string {
	result := []string{}

	for _, source := range c.roots {
		if source.root == nil {
			result = append(result, source.source.Name)
		}
	}

	return result
}

func (s *SnapshotStore) capture(ctx context.Context) (result *snapshotCapture, err error) {
	capture := new(snapshotCapture)

	defer func() {
		if result == nil {
			capture.close()
		}
	}()

	for _, source := range s.sources {
		value, err := openSnapshotRoot(source)
		if err != nil {
			return nil, err
		}

		index := len(capture.roots)

		capture.roots = append(capture.roots, value)
		if value.root == nil {
			continue
		}

		err = fs.WalkDir(value.root.FS(), ".", func(path string, _ fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}

			if walkErr != nil {
				return walkErr
			}

			if path == "." {
				return nil
			}

			return s.captureEntry(capture, index, path)
		})
		if err != nil {
			return nil, err
		}
	}

	return capture, nil
}

func openSnapshotRoot(source SnapshotSource) (snapshotRoot, error) {
	value := snapshotRoot{source: source}

	info, err := os.Lstat(source.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}

	if err != nil {
		return value, err
	}

	if !info.IsDir() {
		return value, ErrInvalidPath
	}

	root, err := os.OpenRoot(source.Directory)
	if err != nil {
		return value, err
	}

	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		_ = root.Close()
		return value, errors.Join(err, errors.New("snapshot source directory changed"))
	}

	value.root, value.info = root, info

	return value, nil
}

func (s *SnapshotStore) captureEntry(capture *snapshotCapture, index int, path string) error {
	capture.entries++
	if capture.entries > snapshotEntryLimit {
		return errors.New("local snapshot has too many entries")
	}

	source := capture.roots[index]
	kind := s.selectPath(source.source.Name, path)

	info, err := source.root.Lstat(filepath.FromSlash(path))
	if err != nil {
		return err
	}

	if kind == SnapshotIgnore {
		if info.IsDir() {
			return fs.SkipDir
		}

		return nil
	}

	if kind == SnapshotDirectory && info.IsDir() {
		return nil
	}

	if kind != SnapshotFile || !info.Mode().IsRegular() {
		return ErrInvalidPath
	}

	if info.Size() > snapshotFileLimit || info.Size() < 0 {
		return errors.New("local snapshot file is too large")
	}

	capture.bytes += info.Size()
	if capture.bytes > snapshotTotalLimit {
		return errors.New("local snapshot exceeds its total size limit")
	}

	capture.files = append(capture.files, snapshotInput{source: index, area: source.source.Name, path: path, info: info})

	return nil
}

func (c *snapshotCapture) equal(other *snapshotCapture) bool {
	if len(c.files) != len(other.files) || len(c.roots) != len(other.roots) {
		return false
	}

	for i, source := range c.roots {
		again := other.roots[i]
		if source.root == nil && again.root == nil {
			continue
		}

		if source.root == nil || again.root == nil || !os.SameFile(source.info, again.info) {
			return false
		}
	}

	for i, file := range c.files {
		again := other.files[i]
		if file.area != again.area || file.path != again.path || !sameSnapshotFile(file.info, again.info) {
			return false
		}
	}

	return true
}

func sameSnapshotFile(a, b os.FileInfo) bool {
	return b != nil && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
