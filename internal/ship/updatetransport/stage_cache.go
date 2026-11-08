package updatetransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

func prepareStage(ctx context.Context, cache, source string) (directory, executable string, err error) {
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", "", err
	} // #nosec G703 -- trusted composition supplies this host cache, never a request.

	if err := hostfile.Protect(cache, true); err != nil {
		return "", "", err
	}

	directory, err = os.MkdirTemp(cache, "stage-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(directory))
		}
	}()

	if err = hostfile.Protect(directory, true); err != nil {
		return directory, "", err
	}

	executable = filepath.Join(directory, "garminctl")
	if runtime.GOOS == platformWindows {
		executable += ".exe"
	}

	err = copyStageCLI(ctx, source, executable)

	return directory, executable, err
}

func copyStageCLI(ctx context.Context, source, target string) (err error) {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPackageBytes {
		return errors.New("stager source must be a bounded regular CLI executable")
	}

	input, err := os.Open(source) //nolint:gosec // trusted local CLI path; Lstat and SameFile reject link/type substitutions.
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()

	opened, err := input.Stat()
	if err != nil {
		return err
	}

	if !os.SameFile(info, opened) {
		return errors.New("stager source changed before copy")
	}

	root, err := os.OpenRoot(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer root.Close()

	output, err := root.OpenFile(filepath.Base(target), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = output.Close() }() // successful Close is checked below

	if err := copyPackage(ctx, output, input, info.Size(), nil); err != nil {
		return err
	}

	if err := output.Sync(); err != nil {
		return err
	}

	if err := output.Close(); err != nil {
		return err
	}

	if err := hostfile.Protect(target, false); err != nil {
		return err
	}

	if runtime.GOOS != platformWindows {
		return os.Chmod(target, 0o700) //nolint:gosec // cached CLI requires owner execute; group/other have no access.
	}

	return nil
}

func writeOperationMarker(directory, operation string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()

	return root.WriteFile(operationMarker, []byte(operation), 0o600)
}

func verifyStagedFiles(ctx context.Context, directory string, release ResolvedRelease, digest string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()

	file, err := root.Open(release.Package.Name)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() || info.Size() != release.Package.Size {
		return errors.New("invalid retained package size or type")
	}

	hash := sha256.New()
	if err := copyPackage(ctx, hash, file, release.Package.Size, nil); err != nil {
		return err
	}

	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("retained package differs from the verified stream")
	}

	signature, err := readStagedSignature(root, release.Signature.Name)
	if err != nil {
		return err
	}

	if len(signature) != 64 {
		return errors.New("invalid retained signature")
	}

	return nil
}

func readStagedSignature(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Size() != 64 {
		return nil, errors.New("invalid retained signature type or size")
	}

	return io.ReadAll(io.LimitReader(file, 65))
}
