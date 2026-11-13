//go:build linux

package updatetransport

import (
	"context"
	"errors"

	"golang.org/x/sys/unix"
)

type installerProcessOwner struct{ fd int }

func openInstallerOwner(pid int) (installerOwner, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, err
	}

	return &installerProcessOwner{fd: fd}, nil
}

func (o *installerProcessOwner) Close() error { return unix.Close(o.fd) }
func (o *installerProcessOwner) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		fds := []unix.PollFd{{Fd: int32(o.fd), Events: unix.POLLIN}} //nolint:gosec // Kernel descriptors are signed C ints; this descriptor came directly from pidfd_open.

		_, err := unix.Poll(fds, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}

		if err != nil {
			return err
		}

		if fds[0].Revents&unix.POLLIN != 0 {
			return nil
		}

		if fds[0].Revents != 0 {
			return errors.New("installer owner handle became invalid")
		}
	}
}
