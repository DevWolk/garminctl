// Package watchsession owns transport-level watch access and cached raw files.
// Feature-specific parsing and caches belong to their feature owners.
package watchsession

import (
	"sync"

	"github.com/DevWolk/garminctl/internal/device"
)

// Session holds one device's raw file cache. The legacy connection owner still
// owns admission and Close until the Manager extraction is complete.
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
