package mcpserver

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
)

func TestHTTPFactorySharesRuntimeAndClosesProtocol(t *testing.T) {
	t.Parallel()
	runtime := garmin.NewRuntime(t.Context(), nil)
	h := NewHTTPServer(runtime, gc.New(filepath.Join(t.TempDir(), "account.json")), "test")

	ts := httptest.NewServer(h)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "shutdown-test", Version: "1"}, nil)

	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	runtime.Stop()

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := session.ListTools(t.Context(), nil); err == nil {
		t.Fatal("MCP accepted request after shutdown")
	}
}
