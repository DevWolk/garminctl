package contracts

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

func TestWatchMCPDecodeRetains400MessageLimit(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
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

	isolateContractAccount(t)
	client := newMCPContractClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })

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

type watchMCPContractCase struct {
	name, tool           string
	args                 map[string]any
	memoryHash, httpHash string
}

func watchMCPContractCases() []watchMCPContractCase {
	return []watchMCPContractCase{
		{name: "info", tool: watchInfoTool, args: map[string]any{}, memoryHash: "4f4f48b07062e1915a75ed6fde5168fba14c99b726e8320a31ae70c30c311149", httpHash: "8317debeeee34c67d5782ea847815accf94abda5eccfe28494257108bbaf1a3d"},
		{name: "files", tool: watchFilesTool, args: map[string]any{watchPathKey: "GARMIN"}, memoryHash: "91efc85b74ebfbbf45fc6dd2b62c496eb96d29a74afa05ca7935fb76baf45ce5", httpHash: "c9de54e985d57a6c9772c76a3acfda3f2c5b7b6025a4ce992ee518e733077289"},
		{name: "decode", tool: watchDecodeTool, args: map[string]any{watchPathKey: "GARMIN/Settings/Settings.fit"}, memoryHash: "41a3c1c09be2b100042e74bd38bb9469fe7a13ea2669da1967efacb6fba33754", httpHash: "5c86f4938286cc44de644451f20d9159025c3d6ccbde30885cb5f49af09bbc58"},
		{name: "delete", tool: watchDeleteTool, args: map[string]any{watchPathKey: "GARMIN/notes.txt"}, memoryHash: "ca5544f6a3d4139b6eaa2efb7382c8c952f4fcb19fe2a1b89e55a8805bb73424", httpHash: "d610c5b2576af8a72b93457af29d9549b76bb14491e383fb54856e55b643e564"},
		{name: "missing file", tool: watchDecodeTool, args: map[string]any{watchPathKey: "GARMIN/missing"}, memoryHash: "b3c26b29f4b96e21d66b751f35f25a7c3f81e5a93247f7042fe5745bc0ab333a", httpHash: "0a845689ef8475140179d09e209e053f52d896405d214bf35c27cde33de04c4c"},
	}
}

func TestWatchMCPContracts(t *testing.T) { //nolint:paralleltest // cases use isolated process account paths.
	for _, test := range watchMCPContractCases() { //nolint:paralleltest // each case configures its account path.
		t.Run(test.name, func(t *testing.T) {
			dev, _ := testdump.Watch(t)
			isolateContractAccount(t)
			client := newMCPContractClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })
			checkMCPResultContract(t, client, test.tool, test.args, test.memoryHash)
		})
	}
}

func TestWatchMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkClientToolContracts(t, newMCPContractClient(t), watchMCPMetadata())
}

func watchMCPMetadata() map[string]string {
	return map[string]string{
		watchInfoTool:   "f0ece6b31a514b32c874fde0b17583013f490e5abc040707add93e9670fe8778",
		watchFilesTool:  "0d64210ee78dbbfb32781d5b7852198386ecc5f39a6aa135470d8391383cf91a",
		watchDecodeTool: "3a556560ed4462558acfc42d4a59dc9af1b6233783822d8ec10b771ba23d423b",
		watchDeleteTool: "219e81c232ce6e7166b55aebe1b992212d09103b58245216b5c02739f5b0be52",
	}
}
