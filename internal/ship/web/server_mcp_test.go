package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/ship/mcpserver"
)

func TestServer_MCP(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-assistant", Version: "1.0"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) < 20 {
		t.Fatalf("tools: %v, error: %v", tools, err)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "watch_list_files", Arguments: map[string]any{"path": "GARMIN"}})
	if err != nil || result.IsError {
		t.Fatalf("tool result: %v, error: %v", result, err)
	}

	snapshot := mcpSnapshot(t, ts)
	if len(snapshot.Clients) != 1 || snapshot.Clients[0].Name != "test-assistant" || !snapshot.Clients[0].SessionActive {
		t.Fatalf("clients: %+v", snapshot.Clients)
	}

	if len(snapshot.Activity) != 1 || snapshot.Activity[0].Tool != "watch_list_files" || snapshot.Activity[0].Failed || snapshot.Activity[0].Running {
		t.Fatalf("activity: %+v", snapshot.Activity)
	}

	checkFailedMCPCall(ctx, t, session, ts)

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	if mcpSnapshot(t, ts).Clients[0].SessionActive {
		t.Fatal("closed session is still active")
	}
}

func checkFailedMCPCall(ctx context.Context, t *testing.T, session *mcp.ClientSession, server *httptest.Server) {
	t.Helper()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "watch_decode_fit", Arguments: map[string]any{"path": "GARMIN/missing.fit"}})
	if err != nil || !result.IsError {
		t.Fatalf("missing file: %v, error: %v", result, err)
	}

	if !mcpSnapshot(t, server).Activity[0].Failed {
		t.Fatal("tool failure was not recorded")
	}
}

func mcpSnapshot(t *testing.T, server *httptest.Server) mcpserver.Snapshot {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	var result struct {
		Status mcpserver.Snapshot `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	return result.Status
}

func TestServer_MCPCrossSiteNavigation(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "navigate")

	response, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status: %d", response.StatusCode)
	}
}
