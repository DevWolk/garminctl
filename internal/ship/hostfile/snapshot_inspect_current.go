package hostfile

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// InspectCurrent verifies a safety copy and its live selected files without
// replacing bytes. Admission and stopped-writer ownership belong to the caller.
// The callback sees only verified, bounded backup files, never arbitrary paths.
func (r *SnapshotRestorer) InspectCurrent(ctx context.Context, safety *Snapshot, inspect func(SnapshotFileInfo, io.Reader) error) error {
	if inspect == nil {
		return errors.New("current data inspection is not configured")
	}

	if err := r.store.validate(ctx, []byte("{}")); err != nil {
		return err
	}

	verified, err := r.verifyExpected(ctx, safety)
	if err != nil {
		return err
	}

	if err := r.checkLive(ctx, verified.Files); err != nil {
		return err
	}

	root, err := os.OpenRoot(verified.Directory)
	if err != nil {
		return err
	}
	defer root.Close()

	for _, info := range verified.Files {
		if r.store.selectPath(info.Area, info.Path) != SnapshotFile {
			return errors.New("inspected data is outside the selected state contract")
		}

		if err := inspectSnapshotFile(ctx, root, info, inspect); err != nil {
			return err
		}
	}

	return r.checkLive(ctx, verified.Files)
}

func inspectSnapshotFile(ctx context.Context, root *os.Root, info SnapshotFileInfo, inspect func(SnapshotFileInfo, io.Reader) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}

	name := filepath.Join(info.Area, filepath.FromSlash(info.Path))
	if err := verifySnapshotCopy(ctx, root, name, info); err != nil {
		return err
	}

	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()

	if err := inspect(info, snapshotReader{ctx: ctx, reader: io.LimitReader(file, info.Bytes+1)}); err != nil {
		return err
	}

	return verifySnapshotCopy(ctx, root, name, info)
}
