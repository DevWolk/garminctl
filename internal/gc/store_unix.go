//go:build !windows

package gc

import (
	"errors"
	"os"
)

func protectStorePath(path string, directory bool) error {
	info, err := os.Lstat(path) //nolint:gosec // local caller-selected account path; reject symlinks before changing permissions
	if err != nil {
		return err
	}

	if directory && info.IsDir() {
		return os.Chmod(path, 0o700) //nolint:gosec // owner-only directory needs its execute bit for traversal
	}

	if !directory && info.Mode().IsRegular() {
		return os.Chmod(path, 0o600) //nolint:gosec // protect the regular account file checked above
	}

	return errors.New("account store must use a regular file in a real directory")
}

func replaceStore(from, to string) error {
	return os.Rename(from, to) //nolint:gosec // replace the local caller-selected store with its same-directory temporary file
}
