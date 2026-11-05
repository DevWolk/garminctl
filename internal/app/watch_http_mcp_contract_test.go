package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestWatchHTTPMCPDecodeRetains400MessageLimit(t *testing.T) { //nolint:paralleltest // the real entry point reads isolated process account paths.
	dev, root := testdump.Watch(t)

	builder := fit.NewBuilder(217, true)
	for i := range uint32(405) {
		if err := builder.Write(20, []fit.FieldDef{{Num: 253, Size: 4, BaseType: fit.BaseUint32}}, map[uint8]any{253: i}); err != nil {
			t.Fatal(err)
		}
	}

	watch, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()

	if err := watch.WriteFile("GARMIN/Activity/many.fit", builder.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	client := newContractClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })

	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: watchDecodeTool, Arguments: map[string]any{watchPathKey: "GARMIN/Activity/many.fit"}})
	if err != nil || result.IsError {
		t.Fatalf("decode result=%+v error=%v", result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var output struct {
		Messages  []fit.NamedMessage `json:"messages"`
		Truncated bool               `json:"truncated"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}

	if len(output.Messages) != 400 || !output.Truncated {
		t.Fatalf("decode limit: messages=%d truncated=%v", len(output.Messages), output.Truncated)
	}
}

// These whole-result digests describe the production stateful HTTP transport
// (protocol 2025-11-25). The original in-memory protocol 2026-07-28 digests
// use the stdio composition; the SDK adds serverInfo/resultType for that protocol.
func TestWatchHTTPMCPContracts(t *testing.T) { //nolint:paralleltest // cases use isolated process account paths.
	for _, test := range watchMCPContractCases() { //nolint:paralleltest // each case configures its account path.
		t.Run(test.name, func(t *testing.T) {
			dev, _ := testdump.Watch(t)
			client := newContractClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })
			checkMCPResultContract(t, client, test.tool, test.args, test.httpHash)
		})
	}
}

func TestWatchHTTPMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkClientToolContracts(t, newContractClient(t), watchMCPMetadata())
}
