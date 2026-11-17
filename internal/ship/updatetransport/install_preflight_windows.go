package updatetransport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// preflightNativeInstall refuses while the installed CLI is in use: Setup
// cannot replace a running executable and would leave a new desktop beside the
// old CLI. The desktop itself is the owner the helper waits for.
func preflightNativeInstall(r installRequest, _ string) error {
	if r.Target.Installation != InstallSetup {
		return nil
	}

	return installedExecutableBusy(r.Install.CLI)
}

// installedExecutableBusy asks for the write access Setup needs, without
// changing the file: Windows refuses it while the image is loaded or another
// program holds the file without sharing.
func installedExecutableBusy(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0) //nolint:gosec // Observed installed CLI path; opened only to probe write access and closed unchanged.
	if err == nil {
		return file.Close()
	}

	return executableBusyError(filepath.Base(path), err, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION, windows.ERROR_USER_MAPPED_FILE)
}

func executableBusyError(name string, err error, busy ...error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	for _, code := range busy {
		if errors.Is(err, code) {
			return fmt.Errorf("%s is running or open in another program, for example an MCP client or a terminal; close it, then install again", name)
		}
	}

	return fmt.Errorf("the installer cannot replace %s: %w", name, err)
}
