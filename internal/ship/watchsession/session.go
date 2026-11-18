// Package watchsession owns transport-level watch access and cached raw files.
// Feature-specific parsing and caches belong to their feature owners.
package watchsession

import (
	"context"
	"sync"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/deviceinfo"
)

// Session provides cached filesystem and optional capability access to one
// device. Its connection owner admits operations and calls Close after joining
// them; Session does not acquire operation leases or start a close worker.
type Session struct {
	dev device.Device

	cacheMu        sync.Mutex
	files          map[string][]byte
	generation     uint64
	reconnectCount int
}

// New wraps the supplied transport without opening or closing it.
func New(dev device.Device) *Session {
	return &Session{dev: dev, files: map[string][]byte{}}
}

// Close releases the device after the connection owner has joined its users.
func (s *Session) Close() error { return s.dev.Close() }

// Abort interrupts in-flight I/O without waiting for its transaction lock.
// The connection owner still joins the operation and calls Close afterwards.
func (s *Session) Abort() {
	if aborter, ok := s.dev.(device.Aborter); ok {
		aborter.Abort()
	}
}

// DeviceXML reads and parses metadata through the generation-aware raw cache.
func (s *Session) DeviceXML(ctx context.Context) (*deviceinfo.DeviceXML, error) {
	b, err := s.ReadThrough(ctx, PathDeviceXML, true)
	if err != nil {
		return nil, err
	}

	return deviceinfo.ParseDeviceXML(b)
}
