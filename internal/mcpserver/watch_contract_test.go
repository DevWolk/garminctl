package mcpserver

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

func TestWatchMCPDecodeRetains400MessageLimit(t *testing.T) {
	t.Parallel()

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

	client := newClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })

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

func TestWatchMCPContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, tool string
		args       map[string]any
		hash       string
	}{
		{name: "info", tool: watchInfoTool, args: map[string]any{}, hash: "9266cea06781c02f0d205e5d3106cb25ac8489095608ffa9e6db76703e5522fa"},
		{name: "files", tool: watchFilesTool, args: map[string]any{watchPathKey: "GARMIN"}, hash: "346166290d61d4f4aa3ceccd82be012cee41a0493d21556cff69a85f63526048"},
		{name: "decode", tool: watchDecodeTool, args: map[string]any{watchPathKey: "GARMIN/Settings/Settings.fit"}, hash: "836b19afe1fead561d8faa9a45f3ecd36139ab069c21a3ef77aaeb78cb8b33b0"},
		{name: "delete", tool: watchDeleteTool, args: map[string]any{watchPathKey: "GARMIN/notes.txt"}, hash: "ca5544f6a3d4139b6eaa2efb7382c8c952f4fcb19fe2a1b89e55a8805bb73424"},
		{name: "missing file", tool: watchDecodeTool, args: map[string]any{watchPathKey: "GARMIN/missing"}, hash: "b3c26b29f4b96e21d66b751f35f25a7c3f81e5a93247f7042fe5745bc0ab333a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dev, _ := testdump.Watch(t)
			client := newClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}

			testdump.CheckJSON(t, result, test.hash)
		})
	}
}

func TestWatchMCPToolContracts(t *testing.T) {
	t.Parallel()

	client := newClient(t)

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
