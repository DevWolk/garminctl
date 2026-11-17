package connect

import "sync"

// deliveredLimit bounds how many written messages one process remembers.
const deliveredLimit = 256

// claim tells a pull what to do with a queued message.
type claim int

const (
	// claimSkip: the message was written and acknowledged.
	claimSkip claim = iota
	// claimBusy: another pull holds the message.
	claimBusy
	// claimDeliver: download, write and acknowledge the message.
	claimDeliver
	// claimAck: the message was written, but Connect was not told; only acknowledge it.
	claimAck
)

// deliveredMessage is a written message and whether Connect was told.
type deliveredMessage struct {
	file  PulledFile
	acked bool
}

// MessageClaims tracks the queued messages pulls of this process are
// delivering or have written to the watch. A pull may work from a listing
// taken before another pull acknowledged a message, so a written message stays
// marked for the next deliveredLimit written messages and is not written again
// meanwhile; when its acknowledgement failed, a later pull only retries that.
// The zero value is usable; a nil receiver lets every pull deliver.
type MessageClaims struct {
	mu        sync.Mutex
	held      map[int64]struct{}
	delivered map[int64]deliveredMessage
	order     []int64 // delivered IDs, oldest first
}

// claim reserves a message for the caller. With claimAck it also returns the
// file an earlier pull wrote.
func (m *MessageClaims) claim(id int64) (claim, PulledFile) {
	if m == nil {
		return claimDeliver, PulledFile{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, taken := m.held[id]; taken {
		return claimBusy, PulledFile{}
	}

	earlier, written := m.delivered[id]
	if written && earlier.acked {
		return claimSkip, PulledFile{}
	}

	if m.held == nil {
		m.held = make(map[int64]struct{})
	}

	m.held[id] = struct{}{}

	if written {
		return claimAck, earlier.file
	}

	return claimDeliver, PulledFile{}
}

// finish ends a claim. A file with a path was written to the watch and stays
// marked, even when Connect was not told, because a second write would
// duplicate it there.
func (m *MessageClaims) finish(file PulledFile, acked bool) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := file.MessageID
	delete(m.held, id)

	if file.Path == "" {
		return
	}

	if m.delivered == nil {
		m.delivered = make(map[int64]deliveredMessage)
	}

	file.Error = ""
	_, known := m.delivered[id]
	m.delivered[id] = deliveredMessage{file: file, acked: acked}

	if known {
		return
	}

	m.order = append(m.order, id)

	if len(m.order) > deliveredLimit {
		delete(m.delivered, m.order[0])
		m.order = m.order[1:]
	}
}
