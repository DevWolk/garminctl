package contracts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const connectLanguageChangeJSON = `{"changes":{"language":2}}`

const (
	connectStatusTool = "cloud_status"
	connectSetTool    = "cloud_set_settings"
	connectSyncTool   = "cloud_sync"
)

func TestConnectMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkToolContracts(t, map[string]string{connectStatusTool: "5039b1c3a6438d43b453bf92715076770edc43d042aa4bba078e4e09f5d05fab", connectSyncTool: "a85d9c8c9e3634d9cb16d35ed76ed0f1a8fa0979f142aed150b6d94e23847204", "cloud_settings": "b53ad63201e0a7fd7e349b51421e2f38228f984aac0ab5ccc7b8b5f8d03714ce", connectSetTool: "c1f4670810175cf8f1a8c1131c1cd91a9896cfd38cc89e14470cc629373c54eb"})
}

func TestConnectMCPContracts(t *testing.T) { //nolint:paralleltest // isolates process cache paths
	for _, test := range []struct { //nolint:paralleltest // cases isolate process cache paths
		name, tool, args, mode string
		normalize              bool
		hash                   string
	}{
		{name: "account status", tool: connectStatusTool, args: `{}`, hash: "cb7e85dc6496bd1bc36df3fa5be6a7999b54b184b70f8364d0f732b50479a5a1"},
		{name: "cloud settings read", tool: "cloud_settings", args: `{}`, hash: "13ba5ae5297ad6ef6937fd232763b3c9246f3cadf67b8c34318f2463df0a7534"},
		{name: "cloud settings failure", tool: "cloud_settings", args: `{}`, mode: "settings read failure", hash: "1c33bc9c57fd13f2cb55ba08992a71002a03fc96cbbf4b05ced07e6762e6282d"},
		{name: "cloud settings apply", tool: connectSetTool, args: connectLanguageChangeJSON, normalize: true, hash: "748b808e01918b0359ae5cea966a64958eaf44722b23c826f67e1c3ec4ba212c"},
		{name: "cloud settings partial", tool: connectSetTool, args: connectLanguageChangeJSON, mode: contractQueueFailure, hash: "86baa4584000092765d588df69ad3729658fd53567d294a04a8eb0e369073e10"},
		{name: "cloud changes empty", tool: connectSetTool, args: `{"changes":{}}`, hash: "944d1aef574f21cd5be8c86cce1d180128e357b13fae78f9de676d4cc54ba70b"},
		{name: "cloud changes forbidden", tool: connectSetTool, args: `{"changes":{"deviceId":9}}`, hash: "c34da262f929e1ae14da0a7f67d94d6fccb2afc3623a9d364539528e71c774a0"},
		{name: "full sync", tool: connectSyncTool, args: `{}`, normalize: true, hash: "27f8452c340e448acc97865285dfc23e5d96e9bbe7be492ba0b854159a8d817e"},
		{name: "no directions", tool: connectSyncTool, args: `{"upload":false,"download":false}`, hash: "13d40621f6479d2351dc83d15e87a3eac71cab338e2e7c684c4cbb8c3267ac3c"},
		{name: "ack error retained", tool: connectSyncTool, args: `{"upload":false}`, mode: ackFailureMode, normalize: true, hash: "a3f3231f4a3a09cee2df41dd86619f842aaa077a948dacbdf878b44c4628d9e1"},
		{name: "upload duplicate", tool: connectSyncTool, args: `{"download":false}`, mode: "duplicate upload", hash: "ad6e3143e6938695b291a7e8f91d1f4982f353e88e27133fcbe5b881173846e0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.HealthEnvironment(t)
			testdump.ConnectCLIEnvironment(t, test.mode)
			root := testdump.ConnectRoot(t)
			client := newMCPContractClientWithOpener(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: json.RawMessage(test.args)})
			if err != nil {
				t.Fatal(err)
			}

			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}

			if test.normalize {
				data = testdump.NormalizePendingPath(t, data)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}
