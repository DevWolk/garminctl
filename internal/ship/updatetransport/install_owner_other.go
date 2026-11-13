//go:build !linux && !windows

package updatetransport

import "errors"

func openInstallerOwner(int) (installerOwner, error) {
	return nil, errors.New("native installer execution requires Windows or Linux")
}
