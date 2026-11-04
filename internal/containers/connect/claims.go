package connect

import "sync"

// MessageClaims marks the queued messages one pull of this process is
// delivering, so a sync job and a bounded pull sharing the runtime never write
// the same message twice. The zero value is usable; a nil receiver claims all.
type MessageClaims struct {
	mu   sync.Mutex
	held map[int64]struct{}
}

func (m *MessageClaims) claim(id int64) bool {
	if m == nil {
		return true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, taken := m.held[id]; taken {
		return false
	}

	if m.held == nil {
		m.held = make(map[int64]struct{})
	}

	m.held[id] = struct{}{}

	return true
}

func (m *MessageClaims) release(id int64) {
	if m == nil {
		return
	}

	m.mu.Lock()
	delete(m.held, id)
	m.mu.Unlock()
}
