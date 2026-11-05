package connect

import "sync"

// deliveredLimit bounds how many written messages one process remembers.
const deliveredLimit = 256

// claim tells a pull what to do with a queued message.
type claim int

const (
	// claimSkip: another pull holds the message, or it was written and acknowledged.
	claimSkip claim = iota
	// claimDeliver: download, write and acknowledge the message.
	claimDeliver
	// claimAck: the message was written, but Connect was not told; only acknowledge it.
	claimAck
)

// MessageClaims tracks the queued messages pulls of this process are
// delivering or have written to the watch. A pull may work from a listing
// taken before another pull acknowledged a message, so a written message stays
// marked for the next deliveredLimit written messages and is not written again
// meanwhile; when its acknowledgement failed, a later pull only retries that.
// The zero value is usable; a nil receiver lets every pull deliver.
type MessageClaims struct {
	mu        sync.Mutex
	held      map[int64]struct{}
	delivered map[int64]bool // written message ID → acknowledged
	order     []int64        // delivered IDs, oldest first
}

func (m *MessageClaims) claim(id int64) claim {
	if m == nil {
		return claimDeliver
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, taken := m.held[id]; taken {
		return claimSkip
	}

	acked, written := m.delivered[id]
	if written && acked {
		return claimSkip
	}

	if m.held == nil {
		m.held = make(map[int64]struct{})
	}

	m.held[id] = struct{}{}

	if written {
		return claimAck
	}

	return claimDeliver
}

// finish ends a claim. A message written to the watch stays marked, even when
// Connect was not told, because a second write would duplicate it there.
func (m *MessageClaims) finish(id int64, written, acked bool) {
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
		m.delivered = make(map[int64]bool)
	}

	if _, ok := m.delivered[id]; ok {
		m.delivered[id] = acked
		return
	}

	m.delivered[id] = acked
	m.order = append(m.order, id)

	if len(m.order) > deliveredLimit {
		delete(m.delivered, m.order[0])
		m.order = m.order[1:]
	}
}
