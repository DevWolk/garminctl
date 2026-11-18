// Package devicehost selects the host's watch transport without changing device policy.
package devicehost

import (
	"context"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/device/mtp"
	"github.com/DevWolk/garminctl/internal/ship/device/wpd"
)

// Open uses a directory when requested, WPD on Windows/WSL, or native USB on Linux.
func Open(ctx context.Context, root, id string) (device.Device, error) {
	if root != "" {
		return device.NewDirFS(root)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if wpd.Available() {
		return wpd.Open(ctx, id)
	}

	return mtp.Open(ctx, id)
}

// Devices lists the devices available through the host transport.
func Devices(ctx context.Context) ([]device.Info, error) {
	if wpd.Available() {
		return wpd.Devices(ctx)
	}

	return mtp.Devices(ctx)
}
