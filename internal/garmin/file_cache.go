package garmin

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/DevWolk/garminctl/internal/ciqstore"
)

// deviceReconnectCount may acquire the transport's lock. It must be called
// outside s.mu, which protects only in-memory caches.
func (s *Service) deviceReconnectCount() int {
	if r, ok := s.dev.(reconnecter); ok {
		return r.Reconnects()
	}

	return 0
}

func (s *Service) observeReconnectLocked(count int) {
	// Observations can arrive out of order when unmanaged callers overlap.
	// Reconnects is a monotonically increasing count on this transport instance.
	if count > s.reconnectCount {
		s.reconnectCount = count
		s.cacheGeneration++
		s.cache = map[string][]byte{}
		s.schemas = map[appSchemaKey]*ciqstore.Schema{}
	}
}

func (s *Service) read(ctx context.Context, p string, cached bool) ([]byte, error) {
	before := s.deviceReconnectCount()
	key := strings.ToLower(p)

	s.mu.Lock()
	s.observeReconnectLocked(before)

	generation := s.cacheGeneration
	if b, ok := s.cache[key]; cached && ok && before == s.reconnectCount {
		data := bytes.Clone(b)
		s.mu.Unlock()

		return data, nil
	}

	s.mu.Unlock()

	b, err := s.dev.Read(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}

	data := bytes.Clone(b)
	after := s.deviceReconnectCount()
	s.mu.Lock()
	s.observeReconnectLocked(after)

	if s.cacheGeneration == generation && before == after && after == s.reconnectCount {
		// cached=false still bypasses lookup but populates the cache, preserving
		// its existing behavior. Neither caller nor transport owns these bytes.
		s.cache[key] = bytes.Clone(data)
	}

	s.mu.Unlock()

	return data, nil
}
