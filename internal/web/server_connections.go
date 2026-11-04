package web

import (
	"net"
	"net/http"
	"sync"
)

// serverConnections tracks sockets that have not started an HTTP request.
// net/http gives these sockets a five-second grace period during Shutdown,
// longer than the backend's shared shutdown budget.
type serverConnections struct {
	mu       sync.Mutex
	unread   map[net.Conn]struct{}
	stopping bool
}

func (c *serverConnections) state(conn net.Conn, state http.ConnState) {
	c.mu.Lock()

	closeConn := state == http.StateNew && c.stopping
	if state == http.StateNew && !c.stopping {
		if c.unread == nil {
			c.unread = make(map[net.Conn]struct{})
		}

		c.unread[conn] = struct{}{}
	} else {
		delete(c.unread, conn)
	}

	c.mu.Unlock()

	if closeConn {
		_ = conn.Close()
	}
}

// closeUnread rejects late accepts and closes only request-free connections.
// Active handlers remain owned and joined by http.Server.Shutdown.
func (c *serverConnections) closeUnread() {
	c.mu.Lock()
	c.stopping = true
	unread := c.unread
	c.unread = nil
	c.mu.Unlock()

	for conn := range unread {
		_ = conn.Close()
	}
}
