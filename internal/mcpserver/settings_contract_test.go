package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const (
	settingsMessageKey = "message"
	settingsReadTool   = "watch_settings"
	settingsSchemaTool = "watch_settings_schema"
	settingsApplyTool  = "watch_set_settings"
	settingsMessage    = "deviceSettings"
)

func TestSettingsMCPContracts(t *testing.T) {
	t.Parallel()

	client := newClient(t)
	for _, test := range []struct {
		name, tool string
		args       map[string]any
		hash       string
	}{
		{name: "default", tool: settingsReadTool, args: map[string]any{}, hash: "82a2bdfedd352da06091841614d115345c3fdc7f21628493326afa38f320e4ea"},
		{name: "all", tool: settingsReadTool, args: map[string]any{"includeUnknown": true}, hash: "ae0c498e31a957e6e664f3bc3f62ce570da846e039a9165f7d459a22b97dd116"},
		{name: "named message", tool: settingsReadTool, args: map[string]any{settingsMessageKey: settingsMessage}, hash: "7e5a71e3c202787cce36e28a4bc8641aa2077be0dd8ae56821f33396980ced99"},
		{name: "numeric message", tool: settingsReadTool, args: map[string]any{settingsMessageKey: "2"}, hash: "7e5a71e3c202787cce36e28a4bc8641aa2077be0dd8ae56821f33396980ced99"},
		{name: "message case", tool: settingsReadTool, args: map[string]any{settingsMessageKey: "DEVICESETTINGS"}, hash: "498f1e8b975f4ae9496188a40a34a7088467f1458d55572cea4faf2a9717d516"},
		{name: "schema", tool: settingsSchemaTool, args: map[string]any{settingsMessageKey: settingsMessage}, hash: "fcf4dbf35ce94bca4aae499a365b1448705bf7708fd1781872c280bece33d032"},
		{name: "schema error", tool: settingsSchemaTool, args: map[string]any{settingsMessageKey: contractMissing}, hash: "deb119063faeefac3af4d5f9a51c291777de3a271f056c37c4f577b2e1518eb4"},
		{name: "empty apply", tool: settingsApplyTool, args: map[string]any{"changes": []any{}}, hash: "24a17f542817b9155900f527cac1f51d544818b179d03c77f195469f6a448fc8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}

			testdump.CheckJSON(t, result, test.hash)
		})
	}

	schemas := map[string]string{
		settingsReadTool:   "e746301cea849ea243ec563b1e207b13a6981544120d32e1e52f373705f24a2b",
		settingsSchemaTool: "899386e1fb7892873a91c03e84268a5fd489f0bd2a97474a44df676a32e4c3c3",
		settingsApplyTool:  "4e2ed3f5efa5096c7d254c3c07013dd414c6987690efc1e3e751ee4c827a9d24",
	}

	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range tools.Tools {
		if tool.Name == settingsReadTool || tool.Name == settingsSchemaTool || tool.Name == settingsApplyTool {
			t.Run(tool.Name+" schema", func(t *testing.T) { t.Parallel(); testdump.CheckJSON(t, tool, schemas[tool.Name]) })
		}
	}
}

func TestSettingsMCPApplyContract(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)
	if err := os.Mkdir(filepath.Join(root, "GARMIN", "NewFiles"), 0o750); err != nil {
		t.Fatal(err)
	}

	client := newClientForRoot(t, root)

	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: settingsApplyTool, Arguments: map[string]any{
		"changes": []any{map[string]any{"mesg": "2", "field": "58", "value": 1500}},
	}})
	if err != nil || result.IsError {
		t.Fatalf("apply result=%+v error=%v", result, err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	data = testdump.NormalizePendingPath(t, data)
	testdump.CheckDigest(t, data, "2b57d744282eaaf1801f0b8951518344b58ab9cad51ab260613decc0337577e3")
}
