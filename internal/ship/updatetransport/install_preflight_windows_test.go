package updatetransport

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBusyInstalledExecutableIsNamedToTheUser(t *testing.T) {
	t.Parallel()

	busy := []error{windows.ERROR_SHARING_VIOLATION}
	sharing := &fs.PathError{Op: "open", Path: `C:\Apps\garminctl.exe`, Err: windows.ERROR_SHARING_VIOLATION}

	if err := executableBusyError("garminctl.exe", sharing, busy...); err == nil || !strings.Contains(err.Error(), "garminctl.exe is running or open in another program") {
		t.Fatalf("sharing violation=%v", err)
	}

	if err := executableBusyError("garminctl.exe", fmt.Errorf("wrapped: %w", fs.ErrNotExist), busy...); err != nil {
		t.Fatalf("missing CLI blocks Setup: %v", err)
	}

	denied := &fs.PathError{Op: "open", Path: `C:\Apps\garminctl.exe`, Err: windows.ERROR_ACCESS_DENIED}
	if err := executableBusyError("garminctl.exe", denied, busy...); err == nil || strings.Contains(err.Error(), "running") {
		t.Fatalf("access denied=%v", err)
	}
}

func TestIdleInstalledExecutableDoesNotBlockSetup(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	path := filepath.Join(root.Name(), "garminctl.exe")
	if err := root.WriteFile("garminctl.exe", []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := installedExecutableBusy(path); err != nil {
		t.Fatalf("idle executable reported busy: %v", err)
	}

	data, err := root.ReadFile("garminctl.exe")
	if err != nil || string(data) != "fixture" {
		t.Fatalf("probe changed the executable: %q %v", data, err)
	}

	held, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(held) }()

	if err := installedExecutableBusy(path); err == nil || !strings.Contains(err.Error(), "running or open in another program") {
		t.Fatalf("exclusively held executable=%v", err)
	}
}
