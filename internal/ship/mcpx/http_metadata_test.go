package mcpx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type metadataInput struct {
	Text string `json:"text"`
}

type metadataOutput struct {
	Reply string `json:"reply"`
}

func TestMetadataKeepsOneHundredCallsWithoutInputsOrResults(t *testing.T) {
	t.Parallel()

	protocol := mcp.NewServer(&mcp.Implementation{Name: "metadata-test", Version: "1"}, nil)
	mcp.AddTool(protocol, &mcp.Tool{Name: "echo"}, func(context.Context, *mcp.CallToolRequest, metadataInput) (*mcp.CallToolResult, metadataOutput, error) {
		return nil, metadataOutput{Reply: "private-result-marker"}, nil
	})

	h := NewHTTPServer(t.Context(), protocol)
	defer func() {
		if err := h.Close(t.Context()); err != nil {
			t.Error(err)
		}
	}()

	ct, st := mcp.NewInMemoryTransports()
	if _, err := protocol.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}

	session, err := mcp.NewClient(&mcp.Implementation{Name: "metadata-client", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	for range 101 {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "private-input-marker"}})
		if err != nil || result.IsError {
			t.Fatalf("synthetic call=%+v error=%v", result, err)
		}
	}

	snapshot := h.Snapshot()
	if len(snapshot.Activity) != 100 || len(snapshot.Clients) != 1 {
		t.Fatalf("metadata calls=%d clients=%d", len(snapshot.Activity), len(snapshot.Clients))
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), "private-input-marker") || strings.Contains(string(data), "private-result-marker") {
		t.Fatalf("arguments/results entered metadata: %s", data)
	}
}

func TestEmptySnapshotRetainsObjectAndEmptyArrays(t *testing.T) {
	t.Parallel()

	protocol := mcp.NewServer(&mcp.Implementation{Name: "metadata-test", Version: "1"}, nil)
	h := NewHTTPServer(t.Context(), protocol)

	data, err := json.Marshal(h.Snapshot())
	if err != nil || string(data) != `{"clients":[],"activity":[]}` {
		t.Fatalf("empty snapshot=%s error=%v", data, err)
	}

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
