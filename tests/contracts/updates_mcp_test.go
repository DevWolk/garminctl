package contracts

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const updateChecksumMode = "checksum"

const checkUpdatesTool = "watch_check_updates"

func TestUpdatesMCPContracts(t *testing.T) { //nolint:paralleltest // replaces process HTTP clients
	for _, test := range []struct { //nolint:paralleltest // cases replace process HTTP clients
		name, tool, mode string
		args             map[string]any
		hash             string
	}{
		{name: "check", tool: checkUpdatesTool, args: map[string]any{}, hash: "971781e8be4ca3fd34c5816ea6b83582864e26d75baef6fb72a80a62d9d5d4d1"},
		{name: "software failure", tool: checkUpdatesTool, mode: "software", args: map[string]any{}, hash: "bb4c246ef9d44eaced3ec803ae6f3ab96486005d7f739f7ec65dde94d4202509"},
		{name: "app failure", tool: checkUpdatesTool, mode: "app", args: map[string]any{}, hash: "33343029b210c20fdbc90f07a2ab960d4c2f17805ea32b86e1ec5b18e2ba4b48"},
		{name: "install", tool: "watch_install_update", args: map[string]any{"partNumber": testdump.UpdatePart}, hash: "b43c2176f8957d73d42be2ec3772d9fa19d3647255d664adf2359d7083581048"},
		{name: updateChecksumMode, tool: "watch_install_update", mode: updateChecksumMode, args: map[string]any{"partNumber": testdump.UpdatePart}, hash: "8992d81046c6bf43fbe3648ee2f82731b678973d75cd1701ee6be4d6036e14e9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := updates.HTTPClient
			updates.HTTPClient = testdump.UpdateEnvironment(t, test.mode)
			t.Cleanup(func() { updates.HTTPClient = previous })
			isolateContractAccount(t)
			client := newMCPContractClientForRoot(t, testdump.UpdateRoot(t))

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}

			testdump.CheckJSON(t, result, test.hash)
		})
	}
}

func TestUpdatesMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkToolContracts(t, map[string]string{checkUpdatesTool: "4c41fd5ff507610a18dad07a2aa57aa2daeeabd12c292aebd43f5c0a5d011259", "watch_install_update": "48ab6fd7e9f181ae7dbb3fd00ad4e8436b90ad263225d75a1a2c88cf5a0dc419"})
}
