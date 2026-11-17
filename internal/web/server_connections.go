package web

import (
	"context"
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
	closed   map[net.Conn]struct{} // closed here; kept until net/http reports the close
	stopping bool
}

type serverConnectionKey struct{}

// attach makes srv report its connection states here and keeps a request from
// running on a connection shutdown has closed. net/http reports a request as
// active only after parsing its headers, so the close can land in between; the
// client gets no response then, and the handler must not act either.
func (c *serverConnections) attach(srv *http.Server) {
	next := srv.Handler

	srv.ConnState = c.state
	srv.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
		return context.WithValue(ctx, serverConnectionKey{}, conn)
	}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, ok := r.Context().Value(serverConnectionKey{}).(net.Conn); ok && c.closedHere(conn) {
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (c *serverConnections) closedHere(conn net.Conn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, closed := c.closed[conn]

	return closed
}

func (c *serverConnections) state(conn net.Conn, state http.ConnState) {
	c.mu.Lock()

	closeConn := state == http.StateNew && c.stopping

	switch {
	case closeConn:
		c.markClosed(conn)
	case state == http.StateNew:
		if c.unread == nil {
			c.unread = make(map[net.Conn]struct{})
		}

		c.unread[conn] = struct{}{}
	default:
		delete(c.unread, conn)

		if state == http.StateClosed || state == http.StateHijacked {
			delete(c.closed, conn)
		}
	}

	c.mu.Unlock()

	if closeConn {
		_ = conn.Close()
	}
}

// markClosed runs with mu held.
func (c *serverConnections) markClosed(conn net.Conn) {
	if c.closed == nil {
		c.closed = make(map[net.Conn]struct{})
	}

	c.closed[conn] = struct{}{}
}

// closeUnread rejects late accepts and closes only request-free connections.
// Active handlers remain owned and joined by http.Server.Shutdown.
func (c *serverConnections) closeUnread() {
	c.mu.Lock()
	c.stopping = true
	unread := c.unread
	c.unread = nil

	for conn := range unread {
		c.markClosed(conn)
	}

	c.mu.Unlock()

	for conn := range unread {
		_ = conn.Close()
	}
}
