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

// backupRegistry publishes a verified private snapshot. Each snapshot owns a
// unique directory, so rapid changes never replace a prior recovery copy.
func backupRegistry(out []byte) (backup string, err error) {
	parent := paths.Cache()
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}

	if err := hostfile.Protect(parent, true); err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp(parent, "OUT.BIN-"+time.Now().Format("20060102-150405")+"-*")
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

	if err := writeRegistrySnapshot(dir, out); err != nil {
		return "", err
	}

	backup = filepath.Join(dir, "OUT.BIN")
	if err := verifyRegistryBackup(backup, out); err != nil {
		return "", err
	}

	return backup, nil
}

func writeRegistrySnapshot(dir string, out []byte) error {
	temp := filepath.Join(dir, ".OUT.BIN.tmp")

	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // fixed basename in our unique protected snapshot directory
	if err != nil {
		return err
	}

	defer func() { _ = f.Close() }()

	if err := hostfile.Protect(temp, false); err != nil {
		return err
	}

	if _, err := f.Write(out); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return hostfile.Replace(temp, filepath.Join(dir, "OUT.BIN"))
}

func verifyRegistryBackup(path string, out []byte) error {
	if err := hostfile.Protect(path, false); err != nil {
		return fmt.Errorf("protect registry backup: %w", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // locally generated protected backup path, never a device or request path
	if err != nil {
		return fmt.Errorf("read registry backup: %w", err)
	}

	if !bytes.Equal(got, out) {
		return errors.New("registry backup readback differs from OUT.BIN")
	}

	return nil
}
