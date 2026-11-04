package apps

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DevWolk/garminctl/internal/hostfile"
	"github.com/DevWolk/garminctl/internal/paths"
)

// backupRegistry publishes a verified private snapshot of OUT.BIN and of the
// pending IN.BIN a change builds on, so a lost queue can be restored. Each
// snapshot owns a unique directory, so rapid changes never replace a prior
// recovery copy. It returns the OUT.BIN copy.
func backupRegistry(out, pending []byte) (backup string, err error) {
	files := map[string][]byte{"OUT.BIN": out}
	if pending != nil {
		files["IN.BIN"] = pending
	}

	dir, err := backupSnapshot("OUT.BIN", files)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "OUT.BIN"), nil
}

// backupSettings publishes a verified private snapshot of an app's .SET file
// before it is rewritten, since the watch replaces the file in place.
func backupSettings(name string, data []byte) (string, error) {
	dir, err := backupSnapshot(name, map[string][]byte{name: data})
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, name), nil
}

// backupSnapshot writes the files into a new protected directory of the cache
// and reads each one back. A failure removes only this snapshot.
func backupSnapshot(prefix string, files map[string][]byte) (dir string, err error) {
	for name := range files {
		if name != filepath.Base(name) || name == "." || name == ".." {
			return "", fmt.Errorf("invalid backup name %q", name)
		}
	}

	parent := paths.Cache()
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}

	if err := hostfile.Protect(parent, true); err != nil {
		return "", err
	}

	dir, err = os.MkdirTemp(parent, prefix+"-"+time.Now().Format("20060102-150405")+"-*")
	if err != nil {
		return "", err
	}

	defer func() {
		if err != nil {
			// Only this invocation's unique snapshot is removed on failure.
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}()

	if err := hostfile.Protect(dir, true); err != nil {
		return "", err
	}

	for name, data := range files {
		if err := writeSnapshotFile(dir, name, data); err != nil {
			return "", err
		}

		if err := verifyRegistryBackup(filepath.Join(dir, name), data); err != nil {
			return "", err
		}
	}

	return dir, nil
}

func writeSnapshotFile(dir, name string, data []byte) error {
	temp := filepath.Join(dir, "."+name+".tmp")

	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // validated basename in our unique protected snapshot directory
	if err != nil {
		return err
	}

	defer func() { _ = f.Close() }()

	if err := hostfile.Protect(temp, false); err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return hostfile.Replace(temp, filepath.Join(dir, name))
}

func verifyRegistryBackup(path string, data []byte) error {
	if err := hostfile.Protect(path, false); err != nil {
		return fmt.Errorf("protect backup: %w", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // locally generated protected backup path, never a device or request path
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}

	if !bytes.Equal(got, data) {
		return fmt.Errorf("backup readback differs from %s", filepath.Base(path))
	}

	return nil
}
