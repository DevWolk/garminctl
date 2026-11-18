//go:build linux && cgo

package mtp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/google/gousb"
)

type usbWire struct {
	context *gousb.Context
	device  *gousb.Device
	config  *gousb.Config
	iface   *gousb.Interface
	in      *gousb.InEndpoint
	out     *gousb.OutEndpoint
}

func (u *usbWire) ReadContext(ctx context.Context, b []byte) (int, error) {
	return u.in.ReadContext(ctx, b)
}

func (u *usbWire) WriteContext(ctx context.Context, b []byte) (int, error) {
	return u.out.WriteContext(ctx, b)
}
func (u *usbWire) PacketSize() int { return u.out.Desc.MaxPacketSize }
func (u *usbWire) Close() error {
	if u.iface != nil {
		u.iface.Close()
	}

	var err error
	if u.config != nil {
		err = u.config.Close()
	}

	if u.device != nil {
		err = errors.Join(err, u.device.Close())
	}

	return errors.Join(err, u.context.Close())
}

// newUSBContext turns the panic gousb raises when libusb_init fails into an error.
func newUSBContext(newContext func() *gousb.Context) (usb *gousb.Context, err error) {
	defer func() {
		if r := recover(); r != nil {
			if cause, ok := r.(error); ok {
				err = fmt.Errorf("initialize libusb: %w", cause)
			} else {
				err = fmt.Errorf("initialize libusb: %v", r)
			}
		}
	}()

	return newContext(), nil
}

func usbInfo(d *gousb.DeviceDesc) device.Info {
	return device.Info{ID: fmt.Sprintf("usb:%03d:%03d", d.Bus, d.Address), Name: fmt.Sprintf("Garmin USB %04x", uint16(d.Product)), Manufacturer: "Garmin"}
}

func classifyUSBOpenError(err error) error {
	if err == nil {
		return nil
	}

	// gousb v1.1.3 formats claim/configuration errors with %v, erasing their
	// error type. Recognize its exact suffix only at this library boundary.
	if errors.Is(err, gousb.ErrorBusy) || strings.HasSuffix(err.Error(), ": "+gousb.ErrorBusy.Error()) ||
		strings.HasSuffix(err.Error(), "is already claimed") {
		return fmt.Errorf("%w: %w", device.ErrBusy, err)
	}

	if errors.Is(err, gousb.ErrorNoDevice) || strings.HasSuffix(err.Error(), ": "+gousb.ErrorNoDevice.Error()) {
		return fmt.Errorf("%w: %w", device.ErrDisconnected, err)
	}

	return err
}

// Devices enumerates only Garmin USB devices without opening or claiming them.
func Devices(ctx context.Context) ([]device.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	usb, err := newUSBContext(gousb.NewContext)
	if err != nil {
		return nil, err
	}
	defer usb.Close() //nolint:errcheck // no devices are opened during enumeration.

	var result []device.Info

	_, err = usb.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		if desc.Vendor == 0x091e {
			result = append(result, usbInfo(desc))
		}

		return false
	})

	return result, err
}

// Open selects a Garmin by USB bus/address, claims its MTP interface and opens a session.
func Open(ctx context.Context, id string) (*Device, error) {
	candidates, err := Devices(ctx)
	if err != nil {
		return nil, err
	}

	selected, err := selectDevice(candidates, id)
	if err != nil {
		return nil, err
	}

	usb, err := newUSBContext(gousb.NewContext)
	if err != nil {
		return nil, err
	}

	u := &usbWire{context: usb}

	devices, err := u.context.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return desc.Vendor == 0x091e && usbInfo(desc).ID == selected.ID
	})
	if len(devices) > 0 {
		u.device = devices[0]
	}

	if err != nil || len(devices) != 1 {
		for i, extra := range devices {
			if i > 0 {
				_ = extra.Close()
			}
		}

		_ = u.Close()

		return nil, fmt.Errorf("open Garmin USB (install the udev rule, then reconnect the watch): %w", errors.Join(classifyUSBOpenError(err), errors.New("device not available")))
	}

	if name, err := u.device.Product(); err == nil {
		selected.Name = strings.TrimSpace(name)
	}

	if err := u.claim(); err != nil {
		_ = u.Close()
		return nil, err
	}

	return newDevice(ctx, u, selected)
}

func (u *usbWire) claim() error {
	for _, config := range u.device.Desc.Configs {
		for _, iface := range config.Interfaces {
			for _, setting := range iface.AltSettings {
				if setting.Class != gousb.ClassImage {
					continue
				}

				return u.claimSetting(config.Number, setting)
			}
		}
	}

	return errors.New("device has no MTP interface; select MTP mode on the watch")
}

func (u *usbWire) claimSetting(config int, setting gousb.InterfaceSetting) error {
	var err error

	u.config, err = u.device.Config(config)
	if err != nil {
		return classifyUSBOpenError(err)
	}

	u.iface, err = u.config.Interface(setting.Number, setting.Alternate)
	if err != nil {
		return fmt.Errorf("claim Garmin MTP interface (close other watch/file-manager connections): %w", classifyUSBOpenError(err))
	}

	for _, endpoint := range setting.Endpoints {
		if endpoint.TransferType != gousb.TransferTypeBulk {
			continue
		}

		if endpoint.Direction == gousb.EndpointDirectionIn {
			u.in, err = u.iface.InEndpoint(endpoint.Number)
		} else {
			u.out, err = u.iface.OutEndpoint(endpoint.Number)
		}

		if err != nil {
			return err
		}
	}

	if u.in == nil || u.out == nil {
		return errors.New("device MTP bulk endpoints are missing")
	}

	return nil
}
