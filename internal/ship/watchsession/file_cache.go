package watchsession

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// reconnecter is implemented by transports that reopen themselves.
type reconnecter interface{ Reconnects() int }

// deviceReconnectCount may acquire the transport lock. Never call it while
// holding cacheMu or a feature cache lock.
func (s *Session) deviceReconnectCount() int {
	if r, ok := s.dev.(reconnecter); ok {
		return r.Reconnects()
	}

	return 0
}

func (s *Session) observeReconnectLocked(count int) {
	// Unmanaged callers may observe this monotonic transport counter out of order.
	if count > s.reconnectCount {
		s.reconnectCount = count
		s.generation++
		s.files = map[string][]byte{}
	}
}

// CacheGeneration samples reconnects and returns the current generation.
// current is false if a later reconnect was already observed by another caller;
// feature caches must not use that observation to look up or publish entries.
// Call this outside any feature cache lock: sampling may wait for the device.
func (s *Session) CacheGeneration() (generation uint64, current bool) {
	count := s.deviceReconnectCount()
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.observeReconnectLocked(count)

	return s.generation, count == s.reconnectCount
}

// Invalidate removes one case-insensitive path and advances the generation so
// an older in-flight read cannot publish. It returns the new generation.
func (s *Session) Invalidate(p string) uint64 {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.generation++
	delete(s.files, strings.ToLower(p))

	return s.generation
}

// Refresh drops all raw files and returns the new generation, without USB I/O.
func (s *Session) Refresh() uint64 {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.generation++
	s.files = map[string][]byte{}

	return s.generation
}

// ReadThrough returns independently owned bytes, populating the raw cache only
// if its generation survives the read. allowCached=false bypasses lookup but
// still populates the cache, so a fresh read also serves later cached reads.
func (s *Session) ReadThrough(ctx context.Context, p string, allowCached bool) ([]byte, error) {
	before := s.deviceReconnectCount()
	key := strings.ToLower(p)

	s.cacheMu.Lock()
	s.observeReconnectLocked(before)

	generation := s.generation
	if b, ok := s.files[key]; allowCached && ok && before == s.reconnectCount {
		data := bytes.Clone(b)
		s.cacheMu.Unlock()

		return data, nil
	}

	s.cacheMu.Unlock()

	b, err := s.dev.Read(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}

	data := bytes.Clone(b)
	after := s.deviceReconnectCount()
	s.cacheMu.Lock()
	s.observeReconnectLocked(after)

	if s.generation == generation && before == after && after == s.reconnectCount {
		s.files[key] = bytes.Clone(data)
	}

	s.cacheMu.Unlock()

	return data, nil
}
