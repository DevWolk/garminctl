package garmin

import "github.com/DevWolk/garminctl/internal/ciqstore"

// The requested version is retained even when Load falls back to a newer schema.
// A Service owns one transport; reconnect observations invalidate its entries.
type appSchemaKey struct {
	appID      string
	version    int
	partNumber string
	unit       uint64
}

func (s *Service) cachedAppSchema(key appSchemaKey, generation uint64, reconnects int) *ciqstore.Schema {
	current := s.deviceReconnectCount()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.observeReconnectLocked(current)

	if generation != s.cacheGeneration || current != reconnects || current != s.reconnectCount {
		return nil
	}

	return s.schemas[key]
}

func (s *Service) rememberAppSchema(key appSchemaKey, generation uint64, reconnects int, sc *ciqstore.Schema) {
	current := s.deviceReconnectCount()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.observeReconnectLocked(current)

	if generation == s.cacheGeneration && current == reconnects && current == s.reconnectCount {
		s.schemas[key] = sc
	}
}
