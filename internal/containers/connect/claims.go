package connect

import "sync"

// deliveredLimit bounds how many written messages one process remembers.
const deliveredLimit = 256

// MessageClaims tracks the queued messages pulls of this process are
// delivering or have written to the watch. A pull may work from a listing
// taken before another pull acknowledged a message, so a written message stays
// marked and is never written again by this process. The zero value is
// usable; a nil receiver claims all.
type MessageClaims struct {
	mu        sync.Mutex
	held      map[int64]struct{}
	delivered map[int64]struct{}
	order     []int64 // delivered IDs, oldest first
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

	if _, done := m.delivered[id]; done {
		return false
	}

	if m.held == nil {
		m.held = make(map[int64]struct{})
	}

	m.held[id] = struct{}{}

	return true
}

// finish ends a claim. A message written to the watch stays marked, even when
// Connect was not told, because a second write would duplicate it there.
func (m *MessageClaims) finish(id int64, written bool) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.held, id)

	if !written {
		return
	}

	if m.delivered == nil {
		m.delivered = make(map[int64]struct{})
	}

	if _, ok := m.delivered[id]; ok {
		return
	}

	m.delivered[id] = struct{}{}
	m.order = append(m.order, id)

	if len(m.order) > deliveredLimit {
		delete(m.delivered, m.order[0])
		m.order = m.order[1:]
	}
}
