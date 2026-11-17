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
		{name: "check", tool: checkUpdatesTool, args: map[string]any{}, hash: "b445fc0aabc27810dbb79c7118aa8fc0a8821eac22918e4684ef168772701cbd"},
		{name: "software failure", tool: checkUpdatesTool, mode: "software", args: map[string]any{}, hash: "57664224976a84a0a5d50b0c7da5f724a9fc905a8d445bdd15fd2cf7661099a1"},
		{name: "app failure", tool: checkUpdatesTool, mode: "app", args: map[string]any{}, hash: "492e1f5f5a4fad9855a7ef033ce8ebcb37401a3ac181a340170b6897868ea63d"},
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
