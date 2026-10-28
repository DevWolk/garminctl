package mcpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHTTPMCPCloseDrainsProtocolSessions(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()

	protocol := mcp.NewServer(&mcp.Implementation{Name: "tracker-test", Version: "1"}, nil)
	h := NewHTTPServer(parent, protocol)

	ts := httptest.NewServer(h)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "shutdown-test", Version: "1"}, nil)

	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	cancelParent()

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := session.ListTools(t.Context(), nil); err == nil {
		t.Fatal("MCP accepted request after shutdown")
	}

	for range h.server.Sessions() {
		t.Fatal("SDK session remained after close")
	}
}

func TestHTTPMCPCloseRetainsIncompleteRequest(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()

	protocol := mcp.NewServer(&mcp.Implementation{Name: "tracker-test", Version: "1"}, nil)
	h := NewHTTPServer(parent, protocol)
	entered, gate := make(chan struct{}), make(chan struct{})

	release := sync.OnceFunc(func() { close(gate) })
	defer release()

	h.handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { close(entered); <-gate; w.WriteHeader(http.StatusOK) })

	done := make(chan struct{})
	go func() {
		defer close(done)

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil))
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not enter handler")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := h.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished close: %v", err)
	}

	rejected := httptest.NewRecorder()
	h.ServeHTTP(rejected, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil))

	if rejected.Code != http.StatusServiceUnavailable {
		t.Fatalf("new request admitted: %d", rejected.Code)
	}

	release()
	<-done

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
