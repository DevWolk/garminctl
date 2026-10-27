//go:build !linux || !cgo

package mtp

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/device"
)

// Devices reports that this build lacks the native USB transport.
func Devices(_ context.Context) ([]device.Info, error) {
	return nil, errors.New("native USB needs the Linux package built with libusb; Windows/WSL use WPD")
}

// Open reports that this build lacks the native USB transport.
func Open(_ context.Context, _ string) (*Device, error) {
	return nil, errors.New("native USB needs the Linux package built with libusb; use -root DIR for a dump")
}
