package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerConnectionsShutdown(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		active bool
		late   bool
	}{
		{name: "unread"},
		{name: "active request", active: true},
		{name: "late accept", late: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			conn, peer := net.Pipe()
			defer func() { _ = conn.Close(); _ = peer.Close() }()

			if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}

			connections := new(serverConnections)
			if test.late {
				connections.closeUnread()
			}

			connections.state(conn, http.StateNew)

			if test.active {
				connections.state(conn, http.StateActive)
			}

			connections.closeUnread()

			done := make(chan error, 1)

			go func() { _, err := conn.Write([]byte{'x'}); done <- err }()

			var data [1]byte

			n, err := peer.Read(data[:])
			if test.active {
				if err != nil || n != 1 || data[0] != 'x' {
					t.Fatalf("shutdown closed an active request: n=%d data=%q err=%v", n, data, err)
				}
			} else if !errors.Is(err, io.EOF) {
				t.Fatalf("request-free socket survived shutdown: %v", err)
			}

			select {
			case err := <-done:
				if (err == nil) != test.active {
					t.Fatalf("active=%v connection write=%v", test.active, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("socket writer did not join")
			}
		})
	}
}

func TestRequestOnConnectionClosedByShutdownDoesNotRun(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		// closedByShutdown: shutdown closes the socket after net/http parsed the
		// request headers and before it reports the request as active.
		closedByShutdown bool
	}{
		{name: "request accepted before shutdown"},
		{name: "socket closed before the request became active", closedByShutdown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool

			connections := new(serverConnections)
			srv := &http.Server{
				ReadHeaderTimeout: 5 * time.Second,
				Handler:           http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran.Store(true) }),
			}
			connections.attach(srv)

			if test.closedByShutdown {
				tracked := srv.ConnState
				srv.ConnState = func(conn net.Conn, state http.ConnState) {
					if state == http.StateActive {
						connections.closeUnread()
					}

					tracked(conn, state)
				}
			}

			listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			served := make(chan error, 1)

			go func() { served <- srv.Serve(listener) }()

			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+listener.Addr().String()+"/api/refresh", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}

			client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}

			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
			}

			if (err != nil) != test.closedByShutdown {
				t.Fatalf("response error = %v, closed by shutdown = %t", err, test.closedByShutdown)
			}

			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
			defer cancel()

			// Shutdown joins the handler, so the flag is final afterwards.
			if err := srv.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}

			if err := <-served; !errors.Is(err, http.ErrServerClosed) {
				t.Fatal(err)
			}

			if ran.Load() == test.closedByShutdown {
				t.Fatalf("handler ran = %t, closed by shutdown = %t", ran.Load(), test.closedByShutdown)
			}
		})
	}
}
