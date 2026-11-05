package web

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
)

func TestServeRuntimeClosesUnreadConnections(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var config net.ListenConfig

	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	reading := make(chan struct{})
	watch := process.New(ctx, func(context.Context) (device.Device, error) {
		t.Error("unread HTTP connection opened a watch")
		return nil, errors.New("unexpected watch open")
	})
	account := gc.New(filepath.Join(t.TempDir(), "account.json"))
	done := make(chan struct{})

	var serveErr error

	go func() {
		defer close(done)

		serveErr = ServeRuntime(&headerReadListener{Listener: listener, started: sync.OnceFunc(func() { close(reading) })}, watch, account, false, "test", Features{})
	}()

	t.Cleanup(func() {
		cancel()

		_ = listener.Close()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("HTTP server did not join")
		}
	})

	var dialer net.Dialer

	conn, err := dialer.DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// A browser may connect without sending a request. Wait until net/http is
	// reading that connection so cancellation cannot race listener acceptance.
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not begin reading the connection")
	}

	cancel()

	select {
	case <-done:
		if serveErr != nil {
			t.Fatalf("unread connection prevented clean shutdown: %v", serveErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unread connection delayed shutdown")
	}
}

type headerReadListener struct {
	net.Listener
	started func()
}

func (l *headerReadListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &headerReadConn{Conn: conn, started: l.started}, nil
}

type headerReadConn struct {
	net.Conn
	started func()
}

func (c *headerReadConn) Read(p []byte) (int, error) {
	c.started()
	return c.Conn.Read(p)
}
