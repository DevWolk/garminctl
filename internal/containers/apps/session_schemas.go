package apps

import (
	"sync"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// SessionSchemas owns the decorated-schema cache of a watch owner's newest
// connection. A caller holding an older or unknown Session gets a private
// cache that is not retained, so a delayed snapshot cannot evict the current
// cache and no retired connection stays referenced. The zero value is usable.
type SessionSchemas struct {
	mu     sync.Mutex
	closed bool
	epoch  watchsession.ConnectionEpoch
	source *watchsession.Session
	cache  *SchemaCache
}

// For returns the cache for session. epoch is the owner's connection epoch
// when session is its current connection, and zero otherwise.
func (s *SessionSchemas) For(session *watchsession.Session, epoch watchsession.ConnectionEpoch) *SchemaCache {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session != nil && session == s.source {
		return s.cache
	}

	cache := new(SchemaCache)
	if session != nil && !s.closed && epoch > s.epoch {
		s.epoch, s.source, s.cache = epoch, session, cache
	}

	return cache
}

// Close drops the retained cache; later calls get private caches.
func (s *SessionSchemas) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	s.source, s.cache = nil, nil
}
