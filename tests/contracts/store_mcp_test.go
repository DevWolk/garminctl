package contracts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

const storeFilteredSearchCase = "filtered search"

const storeSearchTool = "store_search"

func TestStoreMCPContracts(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	for _, test := range []struct { //nolint:paralleltest // cases replace the process store client
		name, tool, fail string
		args             map[string]any
		hash             string
	}{
		{name: "store list", tool: storeSearchTool, args: map[string]any{}, hash: "5770d1aa4a061b9f329ab46d297a835cae74a0e8cf35d637498939ce80767a51"},
		{name: storeFilteredSearchCase, tool: storeSearchTool, args: map[string]any{"query": "face", "type": "watchface", "page": 2}, hash: "ffe71bb08771c1aabcc736f84b8e04be31f43de449390d55e2f7e835b9841fbd"},
		{name: "detail", tool: "store_app", args: map[string]any{"id": testdump.StoreAppID}, hash: "3beea8eeac47f58044e7d0c05754ea481f81f2ddd6ac802c743c6b44817fa3ca"},
		{name: "reviews unavailable", tool: "store_app", args: map[string]any{"id": testdump.StoreAppID}, fail: "/apps/" + testdump.StoreAppID + "/reviews", hash: "59e813b0a468fe4f336f2b02d8a9a9366e02fd04b9e6be5b485facdb5acfa4a5"},
		{name: "browse unavailable", tool: storeSearchTool, args: map[string]any{}, fail: "/apps", hash: "6cf494fd0dd408531010d070fa218f0f8f2cff98073b3b1c9ba902ef4027a843"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := testdump.StoreEnvironment(t, test.fail)
			root := testdump.Root(t)
			isolateContractAccount(t)
			client := newMCPContractClientWithOpener(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}

			if test.name == storeFilteredSearchCase {
				fixture.CheckPage(t, 20, 40)
			}

			testdump.CheckJSON(t, result, test.hash)
		})
	}
}

func TestStoreMCPRequiresDeviceXML(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	fixture := testdump.StoreEnvironment(t, "")

	root := testdump.Root(t)
	if err := os.Remove(filepath.Join(root, "GARMIN", "GarminDevice.xml")); err != nil {
		t.Fatal(err)
	}

	isolateContractAccount(t)
	client := newMCPContractClientForRoot(t, root)

	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: storeSearchTool, Arguments: map[string]any{}})
	if err != nil || !result.IsError || fixture.Count() != 0 {
		t.Fatalf("result=%+v error=%v requests=%d", result, err, fixture.Count())
	}
}

func TestStoreMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkToolContracts(t, map[string]string{storeSearchTool: "3015884807fda13eeb7da04f028b2af02217613a69356a303d88f17d6e9c83ab", "store_app": "9d86aede301630ac4ad4d2e3b5dbfd95bf9a61dbb49b1ff09849d6adeed784e2"})
}
