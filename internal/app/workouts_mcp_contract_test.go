package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const contractQueueFailure = "queue failure"

const workoutCreateTool = "workout_create"

func TestWorkoutsMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkToolContracts(t, map[string]string{"workouts_list": "97e409aac08a41ffdb9cf810f8efd212612f04328edd015839907f5c4f63a431", workoutCreateTool: "c78697f25076aa48cb771136aee998fdd373bbf49724c5cab9d5af5bbcd71880", "workout_send": "d926e6bd5b25676ec1d36b80d34c182a93550db8d25a8a9e89bf03c7723cc64c"})
}

func TestWorkoutsMCPContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	for _, test := range []struct { //nolint:paralleltest // cases use isolated process account paths and transports.
		name, tool, args, mode string
		normalize              bool
		hash                   string
	}{
		{name: "workout library", tool: "workouts_list", args: `{}`, hash: "f9bbdf5d67b34dd9a3e62ae081a71c0952910186aa7cb13081e3aa6959a428e1"},
		{name: "empty workouts", tool: "workouts_list", args: `{}`, mode: emptyFixtureMode, hash: "8638c9341225a8f59bbb7e823296497a0748bbe970d9ce4f74aafb72d10c315b"},
		{name: "create", tool: workoutCreateTool, args: `{"spec":` + testdump.WorkoutSpecJSON + `}`, normalize: true, hash: "95944d8a0ace56116a5f2e954d022352db175c17e1b8c7927fd4bbcc40f7dd99"},
		{name: "local", tool: workoutCreateTool, args: `{"spec":` + testdump.WorkoutSpecJSON + `,"local":true}`, normalize: true, hash: "b76fee8348c1a7f6d6c4c2a2b3c146dcfd5feed2472b1549b034ed0b530b38db"},
		{name: "partial create", tool: workoutCreateTool, args: `{"spec":` + testdump.WorkoutSpecJSON + `}`, mode: contractQueueFailure, hash: "3dfef41c179fda15ed2260a287c9fe093d1698b6781ce3dfdac72586e774b3b6"},
		{name: "invalid create", tool: workoutCreateTool, args: `{"spec":{"name":"","sport":"running","steps":[]}}`, hash: "c6a8ac10ada4398ff9a8b1577d891802efdfbfed62102a9939c26dd85cf58ae2"},
		{name: "zero id", tool: workoutCreateTool, args: `{"spec":` + testdump.WorkoutSpecJSON + `}`, mode: "zero", hash: "895bfe78c313d25e70440a9bba470561733690470772f27ca29ca14cfcf1b1b7"},
		{name: "send", tool: "workout_send", args: `{"id":42}`, normalize: true, hash: "d8c5747f1cec9a966344196f2ab3f8fb1a21629c0f759cdd2ef8846889e5cd1a"},
		{name: "send failure", tool: "workout_send", args: `{"id":42}`, mode: contractQueueFailure, hash: "2576d86064c53f6c54fcdae41dc6ca4713506f81252174ede7d3723ca7e9da75"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.WorkoutCLIEnvironment(t, test.mode)
			root := testdump.WorkoutRoot(t)
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
