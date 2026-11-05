package app

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDeleteToolInvalidatesWatchMetadata(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	client := newMCPContractClient(t)

	info, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "watch_info"})
	if err != nil || info.IsError {
		t.Fatalf("initial metadata: %+v, %v", info, err)
	}

	const path = "GARMIN/GarminDevice.xml"

	deleted, err := client.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "watch_delete_file", Arguments: map[string]any{watchPathKey: path},
	})
	if err != nil || deleted.IsError {
		t.Fatalf("delete tool failed: %+v, %v", deleted, err)
	}

	data, err := json.Marshal(deleted.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var output struct {
		Deleted string `json:"deleted"`
	}
	if err := json.Unmarshal(data, &output); err != nil || output.Deleted != path {
		t.Fatalf("delete output changed: %s, %v", data, err)
	}

	after, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "watch_info"})
	if err != nil || !after.IsError {
		t.Fatalf("deleted metadata survived in cache: %+v, %v", after, err)
	}
}
