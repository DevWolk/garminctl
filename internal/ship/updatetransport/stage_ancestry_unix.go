//go:build unix

package updatetransport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// checkPrivateAncestry refuses a cache that another local user could redirect:
// every entry on the way to it, links included, must belong to this user or
// root, and no directory on the way may let others rename its entries.
func checkPrivateAncestry(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("private cache requires an absolute path")
	}

	uid := uint32(os.Geteuid()) //nolint:gosec // A process UID is never negative on these platforms.
	current := string(filepath.Separator)
	pending := []string{"."}
	pending = append(pending, strings.Split(path, string(filepath.Separator))...)

	for links := 0; len(pending) > 0; {
		name := pending[0]
		pending = pending[1:]

		switch name {
		case "":
			continue
		case "..":
			current = filepath.Dir(current)
			continue
		}

		next := filepath.Join(current, name)

		info, err := os.Lstat(next)
		if err != nil {
			return err
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot read the owner of %s", next)
		}

		if err := privateAncestor(next, uid, stat.Uid, info.Mode()); err != nil {
			return err
		}

		if info.Mode()&fs.ModeSymlink == 0 {
			current = next
			continue
		}

		if links++; links > 40 {
			return fmt.Errorf("too many links on the way to %s", path)
		}

		target, err := os.Readlink(next)
		if err != nil {
			return err
		}

		if filepath.IsAbs(target) {
			current = string(filepath.Separator)
		}

		pending = append(strings.Split(target, string(filepath.Separator)), pending...)
	}

	return nil
}

func privateAncestor(path string, uid, owner uint32, mode fs.FileMode) error {
	if owner != 0 && owner != uid {
		return fmt.Errorf("refusing to use %s: it belongs to another user", path)
	}

	if mode&fs.ModeSymlink != 0 {
		return nil
	}

	if !mode.IsDir() {
		return fmt.Errorf("refusing to use %s: it is not a directory", path)
	}

	// A sticky directory lets only an entry's owner rename it, and the entry
	// below is checked for its owner in turn.
	if mode.Perm()&0o002 != 0 && mode&fs.ModeSticky == 0 {
		return fmt.Errorf("refusing to use %s: other users can replace its contents", path)
	}

	return nil
}
