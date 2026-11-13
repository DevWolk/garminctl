package updatetransport

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
)

type installerProcessOwner struct{ handle windows.Handle }

func openInstallerOwner(pid int) (installerOwner, error) {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return nil, errors.New("invalid installer parent process")
	}

	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return nil, err
	}

	return &installerProcessOwner{handle: handle}, nil
}
func (o *installerProcessOwner) Close() error { return windows.CloseHandle(o.handle) }
func (o *installerProcessOwner) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		status, err := windows.WaitForSingleObject(o.handle, 100)
		if err != nil {
			return err
		}

		if status == windows.WAIT_OBJECT_0 {
			return nil
		}

		if status != uint32(windows.WAIT_TIMEOUT) {
			return errors.New("invalid installer owner wait result")
		}
	}
}
