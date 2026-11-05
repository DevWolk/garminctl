package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const storeFilteredSearchCase = "filtered search"

const storeSearchTool = "store_search"

func TestStoreMCPContracts(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	for _, test := range []struct { //nolint:paralleltest // cases replace the process store client
		name, tool, fail string
		args             map[string]any
		hash             string
	}{
		{name: "store list", tool: storeSearchTool, args: map[string]any{}, hash: "ddd37187afb8a35511d2eb3c0e2a203f43eddad32af5177b6991655ac5193cfe"},
		{name: storeFilteredSearchCase, tool: storeSearchTool, args: map[string]any{"query": "face", "type": "watchface", "page": 2}, hash: "2b51d535beb13c3f62368f22982d6d1c5fe30ccebce682ead3be9606df6a83cc"},
		{name: "detail", tool: "store_app", args: map[string]any{"id": testdump.StoreAppID}, hash: "56fa92cbc044d0918618b7e15c66816305ffda9f0e75875f4a58f538e30e1746"},
		{name: "reviews unavailable", tool: "store_app", args: map[string]any{"id": testdump.StoreAppID}, fail: "/apps/" + testdump.StoreAppID + "/reviews", hash: "b7311223d680a08ad013fcf6d5f0c8e3303da0c74ccd211f003ce2ea04f763ef"},
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
