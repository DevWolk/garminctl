package hostfile

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

type (
	restoreArea struct {
		source SnapshotSource
		root   *os.Root
		name   string
	}
	restoreStaging struct {
		areas  map[string]*restoreArea
		files  []SnapshotFileInfo
		target map[string]SnapshotFileInfo
		once   bool
		err    error
	}
)

func (s *restoreStaging) close() error {
	if s.once {
		return s.err
	}

	s.once = true
	for _, area := range s.areas {
		s.err = errors.Join(s.err, area.root.RemoveAll(area.name), area.root.Close())
	}

	return s.err
}

func (r *SnapshotRestorer) stage(ctx context.Context, target, safety *Snapshot) (result *restoreStaging, err error) {
	staged := &restoreStaging{areas: make(map[string]*restoreArea), target: restoreFileMap(target.Files)}

	defer func() {
		if result == nil {
			err = errors.Join(err, staged.close())
		}
	}()

	before := restoreFileMap(safety.Files)

	old, err := os.OpenRoot(target.Directory)
	if err != nil {
		return nil, err
	}
	defer old.Close()

	for _, file := range target.Files {
		if previous, ok := before[file.Area+"/"+file.Path]; ok && previous == file {
			continue
		}

		area, err := r.restoreArea(staged, file.Area)
		if err != nil {
			return nil, err
		}

		if err := stageRestoreFile(ctx, old, area, file); err != nil {
			return nil, err
		}

		staged.files = append(staged.files, file)
	}

	for _, file := range safety.Files {
		if _, ok := staged.target[file.Area+"/"+file.Path]; !ok {
			if _, err := r.restoreArea(staged, file.Area); err != nil {
				return nil, err
			}
		}
	}

	return staged, nil
}

func (r *SnapshotRestorer) restoreArea(staged *restoreStaging, name string) (*restoreArea, error) {
	if area := staged.areas[name]; area != nil {
		return area, nil
	}

	for _, source := range r.store.sources {
		if source.Name != name {
			continue
		}

		if err := os.MkdirAll(source.Directory, 0o700); err != nil {
			return nil, err
		}

		if err := Protect(source.Directory, true); err != nil {
			return nil, err
		}

		root, err := os.OpenRoot(source.Directory)
		if err != nil {
			return nil, err
		}

		operation := ".garminctl-restore-" + rand.Text()
		if err := root.Mkdir(operation, 0o700); err != nil {
			return nil, errors.Join(err, root.Close())
		}

		area := &restoreArea{source: source, root: root, name: operation}
		staged.areas[name] = area

		if err := Protect(filepath.Join(source.Directory, operation), true); err != nil {
			return nil, err
		}

		return area, nil
	}

	return nil, errors.New("unknown restore area")
}

func stageRestoreFile(ctx context.Context, source *os.Root, area *restoreArea, file SnapshotFileInfo) error {
	input, err := source.OpenRoot(file.Area)
	if err != nil {
		return err
	}
	defer input.Close()

	info, err := input.Lstat(filepath.FromSlash(file.Path))
	if err != nil {
		return err
	}

	destination, err := area.root.OpenRoot(area.name)
	if err != nil {
		return err
	}
	defer destination.Close()

	value, err := copySnapshotFile(ctx, destination, input, snapshotInput{area: file.Area, path: file.Path, info: info}, filepath.Join(area.source.Directory, area.name))
	if err != nil {
		return err
	}

	if value != file {
		return errors.New("staged restore bytes differ from the reviewed backup")
	}

	return nil
}

func (r *SnapshotRestorer) apply(ctx context.Context, staged *restoreStaging, safety *Snapshot, result *SnapshotRestoreResult) error {
	before := restoreFileMap(safety.Files)

	for _, file := range staged.files {
		if err := ctx.Err(); err != nil {
			return err
		}

		area := staged.areas[file.Area]
		if err := checkSnapshotDestination(area.root, area.source.Directory); err != nil {
			return err
		}

		previous, exists := before[file.Area+"/"+file.Path]

		previous.Path = file.Path
		if err := checkRestoreFile(ctx, area.root, previous, exists); err != nil {
			return err
		}

		if err := area.root.MkdirAll(filepath.Dir(filepath.FromSlash(file.Path)), 0o700); err != nil {
			return err
		}

		if err := protectSnapshotDirectories(area.source.Directory, filepath.Dir(filepath.FromSlash(file.Path))); err != nil {
			return err
		}

		result.Attempted = true
		from := filepath.Join(area.source.Directory, area.name, file.Area, filepath.FromSlash(file.Path))

		to := filepath.Join(area.source.Directory, filepath.FromSlash(file.Path))
		if err := r.replace(from, to); err != nil {
			return err
		}

		result.Replaced++
	}

	return r.removeNewerFiles(ctx, staged, safety, result)
}

func (r *SnapshotRestorer) removeNewerFiles(ctx context.Context, staged *restoreStaging, safety *Snapshot, result *SnapshotRestoreResult) error {
	for _, file := range safety.Files {
		if _, keep := staged.target[file.Area+"/"+file.Path]; keep {
			continue
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		area := staged.areas[file.Area]
		if area == nil {
			return errors.New("restore deletion has no owned staging area")
		}

		if err := checkSnapshotDestination(area.root, area.source.Directory); err != nil {
			return err
		}

		if err := checkRestoreFile(ctx, area.root, file, true); err != nil {
			return err
		}

		result.Attempted = true

		if err := area.root.Remove(filepath.FromSlash(file.Path)); err != nil {
			return err
		}

		result.Removed++
	}

	return nil
}
