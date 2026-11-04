package web

import (
	"errors"
	"io"
	"net"
	"net/http"
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
