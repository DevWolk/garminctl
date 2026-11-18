//go:build linux && cgo

package mtp

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/google/gousb"
)

func TestUSBOpenErrorClassification(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		want error
	}{
		{name: "busy", err: gousb.ErrorBusy, want: device.ErrBusy},
		{name: "disconnected", err: gousb.ErrorNoDevice, want: device.ErrDisconnected},
		{name: "claim loses error type", err: fmt.Errorf("failed to claim interface 0: %w", gousb.ErrorBusy), want: device.ErrBusy},
		{name: "configuration loses error type", err: fmt.Errorf("configuration: %w", gousb.ErrorNoDevice), want: device.ErrDisconnected},
		{name: "access denied retains cause", err: gousb.ErrorAccess, want: gousb.ErrorAccess},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := classifyUSBOpenError(tt.err)
			if !errors.Is(err, tt.want) || !errors.Is(err, tt.err) {
				t.Fatalf("classification = %v", err)
			}
		})
	}
}

func TestUSBContextInitPanicBecomesError(t *testing.T) {
	t.Parallel()

	usb, err := newUSBContext(func() *gousb.Context { panic(gousb.ErrorAccess) })
	if usb != nil || !errors.Is(err, gousb.ErrorAccess) {
		t.Fatalf("libusb init failure = %v", err)
	}

	if _, err := newUSBContext(func() *gousb.Context { panic("no libusb") }); err == nil {
		t.Fatal("non-error panic was not reported")
	}
}
