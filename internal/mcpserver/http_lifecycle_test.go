package mcpserver

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHTTPFactorySharesLifetimeAndClosesProtocol(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()

	h := NewHTTPServer(parent, "test")

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
}
