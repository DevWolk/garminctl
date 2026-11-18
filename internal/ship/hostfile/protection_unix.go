//go:build !windows

package hostfile

import (
	"os"
)

// Protect restricts an existing real directory/file to the current owner.
func Protect(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if directory && info.IsDir() {
		return os.Chmod(path, 0o700) //nolint:gosec // owner-only directory needs its execute bit for traversal
	}

	if !directory && info.Mode().IsRegular() {
		return os.Chmod(path, 0o600)
	}

	return ErrInvalidPath
}

// Replace renames a same-directory temporary file over the previous path.
func Replace(from, to string) error {
	return os.Rename(from, to)
}
