package garmin

import (
	"context"
	"sync"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Opener connects to the device on demand and bounds its own waiting.
type Opener = watchsession.Opener

// ConnectionEpoch identifies a managed connection independently of raw caches.
type ConnectionEpoch = watchsession.ConnectionEpoch

// ErrSessionChanged identifies a retired or replaced connection.
var ErrSessionChanged = watchsession.ErrSessionChanged

// Lazy preserves the legacy feature facade over the shared Session Manager.
// It owns no transport, admission permits or shutdown worker.
type Lazy struct {
	manager *watchsession.Manager

	mu           sync.Mutex
	facadeClosed bool
	facadeEpoch  ConnectionEpoch
	facadeSource *watchsession.Session
	facadeValue  *Service
}

// NewLazy creates a standalone owner with its own admission gate.
func NewLazy(open Opener) *Lazy { return NewLazyWithGate(open, new(lifecycle.Gate)) }

// NewLazyWithGate shares operation/job/maintenance admission with the runtime.
func NewLazyWithGate(open Opener, gate *lifecycle.Gate) *Lazy {
	return &Lazy{manager: watchsession.NewManagerWithGate(open, gate)}
}

// Manager supplies the feature-free owner to newly extracted adapters.
func (l *Lazy) Manager() *watchsession.Manager { return l.manager }

// Gate is the shared operation/job/maintenance admission owner.
func (l *Lazy) Gate() *lifecycle.Gate { return l.manager.Gate() }

// CurrentEpoch is a pure connection snapshot; zero means no open connection.
func (l *Lazy) CurrentEpoch() ConnectionEpoch { return l.manager.CurrentEpoch() }

// Current returns a legacy unleased facade. Acquire before performing I/O.
func (l *Lazy) Current() *Service {
	session, epoch := l.manager.Snapshot()
	return l.facade(session, epoch)
}

// AppSchemaCacheFor is a temporary feature-state bridge for extracted adapters.
// The caller must hold a Manager lease for the supplied non-nil Session. It
// borrows the canonical facade's cache without acquiring again or exposing I/O.
func (l *Lazy) AppSchemaCacheFor(session *watchsession.Session) *apps.SchemaCache {
	current, epoch := l.manager.Snapshot()
	if current != session {
		epoch = 0 // a retiring Session must not replace a newer facade
	}

	return &l.facade(session, epoch).schemas
}

// facade retains only the newest observed connection. A delayed unleased
// snapshot cannot evict its facade; no map retains every retired connection.
func (l *Lazy) facade(session *watchsession.Session, epoch ConnectionEpoch) *Service {
	if session == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if session == l.facadeSource {
		return l.facadeValue
	}

	value := NewFromSession(session)
	if !l.facadeClosed && epoch > l.facadeEpoch {
		l.facadeEpoch, l.facadeSource, l.facadeValue = epoch, session, value
	}

	return value
}

// Get is a legacy bridge; it does not reserve later I/O on the returned facade.
func (l *Lazy) Get(ctx context.Context) (*Service, error) {
	lease, err := l.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	return lease.Service(), lease.Release()
}

// Drop invalidates the current connection; leased callers use DropAt.
func (l *Lazy) Drop() { l.manager.Drop() }

// DropAt invalidates only the matching connection epoch.
func (l *Lazy) DropAt(epoch ConnectionEpoch) error { return l.manager.DropAt(epoch) }

// Close cancels and joins the Manager's existing cleanup worker.
func (l *Lazy) Close(ctx context.Context) error {
	err := l.manager.Close(ctx)
	l.mu.Lock()
	l.facadeClosed = true
	l.facadeSource, l.facadeValue = nil, nil
	l.mu.Unlock()

	return err
}
