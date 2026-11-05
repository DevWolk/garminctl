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

const (
	watchInfoTool   = "watch_info"
	watchFilesTool  = "watch_list_files"
	watchDecodeTool = "watch_decode_fit"
	watchDeleteTool = "watch_delete_file"
	watchPathKey    = "path"
)

// These whole-result digests describe the production stateful HTTP transport
// (protocol 2025-11-25). The original in-memory protocol 2026-07-28 digests
// remain in mcpserver; the SDK adds serverInfo/resultType only for that protocol.
func TestWatchHTTPMCPContracts(t *testing.T) { //nolint:paralleltest // the real entry point reads isolated process account paths.
	for _, test := range []struct { //nolint:paralleltest // each case isolates process account paths.
		name, tool string
		args       map[string]any
		hash       string
	}{
		{name: "info", tool: watchInfoTool, args: map[string]any{}, hash: "ebbbccfa42e8967881446a0cf8b77b3d6eb9ad55fa67fa88eaac355b1be03315"},
		{name: "files", tool: watchFilesTool, args: map[string]any{watchPathKey: "GARMIN"}, hash: "73e01a5e57a9bc8677967d25b7d0563f618684191123f72f228f908d4a05653d"},
		{name: "decode", tool: watchDecodeTool, args: map[string]any{watchPathKey: "GARMIN/Settings/Settings.fit"}, hash: "22f35b9200cf7185279193baec78540046beb0fa092ab60368758f30887c50bf"},
		{name: "delete", tool: watchDeleteTool, args: map[string]any{watchPathKey: "GARMIN/notes.txt"}, hash: "d610c5b2576af8a72b93457af29d9549b76bb14491e383fb54856e55b643e564"},
		{name: "missing file", tool: watchDecodeTool, args: map[string]any{watchPathKey: "GARMIN/missing"}, hash: "0a845689ef8475140179d09e209e053f52d896405d214bf35c27cde33de04c4c"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dev, _ := testdump.Watch(t)
			client := newContractClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}

			testdump.CheckJSON(t, result, test.hash)
		})
	}
}

func TestWatchHTTPMCPToolContracts(t *testing.T) { //nolint:paralleltest // the real entry point reads isolated process account paths.
	client := newContractClient(t)

	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		watchInfoTool:   "f0ece6b31a514b32c874fde0b17583013f490e5abc040707add93e9670fe8778",
		watchFilesTool:  "0d64210ee78dbbfb32781d5b7852198386ecc5f39a6aa135470d8391383cf91a",
		watchDecodeTool: "3a556560ed4462558acfc42d4a59dc9af1b6233783822d8ec10b771ba23d423b",
		watchDeleteTool: "219e81c232ce6e7166b55aebe1b992212d09103b58245216b5c02739f5b0be52",
	}
	seen := 0

	for _, tool := range tools.Tools {
		if hash, ok := want[tool.Name]; ok {
			seen++

			t.Run(tool.Name, func(t *testing.T) { t.Parallel(); testdump.CheckJSON(t, tool, hash) })
		}
	}

	if seen != len(want) {
		t.Fatalf("registered watch tools=%d want=%d", seen, len(want))
	}
}
