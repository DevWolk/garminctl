package updatetransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

func openPackageFile(root *os.Root, name string, size int64) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}

	if !before.Mode().IsRegular() || before.Size() != size {
		return nil, errors.New("retained package file size or type does not match")
	}

	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) {
		_ = file.Close()
		return nil, errors.Join(err, errors.New("retained file changed before open"))
	}

	return file, nil
}

func copyRetainedFile(ctx context.Context, source *os.Root, from, directory, to string, size int64) (err error) {
	input, err := openPackageFile(source, from, size)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()

	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}

	defer root.Close()

	output, err := root.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = output.Close() }()

	if err := hostfile.Protect(filepath.Join(directory, to), false); err != nil {
		return err
	}

	if err := copyPackage(ctx, output, input, size, nil); err != nil {
		return err
	}

	if err := output.Sync(); err != nil {
		return err
	}

	return output.Close()
}

func (s *Stager) verifyRetained(ctx context.Context, target MatchRequest, release ResolvedRelease, directory string, progress func(StageProgress)) (*StagedPackage, error) {
	prepare := func(ctx context.Context, operation string) error {
		root, err := os.OpenRoot(directory)
		if err != nil {
			return err
		}
		defer root.Close()

		input := filepath.Join(operation, verifyInput)
		if err := os.Mkdir(input, 0o700); err != nil {
			return err
		} // #nosec G703 -- child of our newly created private operation.

		if err := hostfile.Protect(input, true); err != nil {
			return err
		}

		if err := copyRetainedFile(ctx, root, release.Package.Name, input, "package", release.Package.Size); err != nil {
			return err
		}

		return copyRetainedFile(ctx, root, release.Signature.Name, input, "signature", 64)
	}

	return s.run(ctx, target, release, true, prepare, progress)
}
