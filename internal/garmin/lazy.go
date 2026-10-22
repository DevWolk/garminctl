package garmin

import (
	"context"
	"sync"

	"github.com/DevWolk/garminctl/internal/device"
)

// Opener connects to the device on demand. It bounds its own waiting.
type Opener func(ctx context.Context) (device.Device, error)

// Lazy opens the watch on first use and hands the same Service to every
// caller until Drop. Only one open runs at a time; readers of the current
// state never wait for it. The web and MCP servers share it.
type Lazy struct {
	open   Opener
	mu     sync.Mutex // guards svc
	openMu sync.Mutex // one connection attempt at a time
	svc    *Service
}

// NewLazy wraps open.
func NewLazy(open Opener) *Lazy { return &Lazy{open: open} }

// Current returns the connected service, or nil.
func (l *Lazy) Current() *Service {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.svc
}

// Get returns the connected service, opening the device when needed.
func (l *Lazy) Get(ctx context.Context) (*Service, error) {
	if svc := l.Current(); svc != nil {
		return svc, nil
	}

	l.openMu.Lock()
	defer l.openMu.Unlock()

	if svc := l.Current(); svc != nil {
		return svc, nil
	}

	dev, err := l.open(ctx)
	if err != nil {
		return nil, err
	}

	svc := New(dev)

	l.mu.Lock()
	l.svc = svc
	l.mu.Unlock()

	return svc, nil
}

// Drop closes the device, if open; the next Get reconnects.
func (l *Lazy) Drop() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.svc != nil {
		_ = l.svc.Close()
		l.svc = nil
	}
}
