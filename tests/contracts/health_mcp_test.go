package contracts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const activityDetailTool = "activity_detail"

func TestHealthMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkToolContracts(t, map[string]string{"activities_list": "fdac9bb1338bdb9dd294429d92b277f609355203677afa7f2725d5b88c982c50", activityDetailTool: "50e9487963d02cc18aee47f5ca7fc08aff2dfe3c2b19782142bda2d5c645cecd", "health_daily": "07254d2d2773764cfcb6bece47c80a882da50ac0073ba4a4e0ea298338e8b15c", "health_history": "6915a3e1f6d570f7ccfde9e0fca3a441aa4c7aa131cd6d2f7b02196ff16053be", "health_extras": "46273524089714d3b948d043423509495fd6dae6c01db60419bca283f72c7436", "health_log_weight": "5c0f68b7625e9f366a301b53cb4d1937c215dece87e12822cd74e2837bfb1ca9", "health_day": "a1e0f39416f1becca5f4732c5c46dfa0ccaf244b04b12e751a84ad31a9abc37e"})
}

func TestHealthMCPContracts(t *testing.T) { //nolint:paralleltest // isolates process cache paths
	for _, test := range []struct { //nolint:paralleltest // cases isolate process state
		name, tool, args, mode string
		hash                   string
	}{
		{name: "activity list", tool: "activities_list", args: `{"limit":1}`, hash: "83f61d3b34f8ad66aa1bf075ca1efe9df253ac1cd68629312aac5c2ae49c4a6a"},
		{name: "track detail", tool: activityDetailTool, args: `{"file":"fixture.fit","points":2}`, hash: "0849c94cb51dbd7b1251f97cd0092b2e9103576959a8bed3cbb76d48040189c5"},
		{name: "laps only", tool: activityDetailTool, args: `{"file":"fixture.fit","points":-1}`, hash: "b369467ebff41ab3e60fd75cfcbb17f35e2e87af6950e2e4eaf24f81dd3cba53"},
		{name: "invalid activity", tool: activityDetailTool, args: `{"file":"bad..fit"}`, hash: "aa0e78cdd24b1dc6ad3997cfe24f18a21b376610eeca7d30c83b79ca8bd63018"},
		{name: "daily metrics", tool: "health_daily", args: `{"days":2}`, hash: "7c8539bb62fd0ac51508909a64aee19117cc4dc38400d512a069a6aa70a29634"},
		{name: "range history", tool: "health_history", args: `{"days":3}`, hash: "e6366333c6e7195f7a0e4299bd3bb41ee7078f7029cf50326dff2427b83b6b21"},
		{name: "daily errors", tool: "health_daily", args: `{"days":2}`, mode: "daily error", hash: "d56721a548ffb0527bda13b4f5588526d551beaaf471cd8ce76de3820e7fa998"},
		{name: "intraday series", tool: "health_day", args: `{"date":"2000-01-02"}`, hash: "61c92521c14c732d5c5b394f50d6d6ff165bfda89cf1afb619cf248f973b3b19"},
		{name: "intraday partial", tool: "health_day", args: `{"date":"2000-01-02"}`, mode: "partial day", hash: "f92be6cb361584f1f7a0a1e1a6e8281ff88b785d0cf9c32b66c9b33564136af9"},
		{name: "fitness", tool: "health_extras", args: `{}`, hash: "7b98d439ff4951d78ebb39495f6b4304c349e69de0a5c3757393eb7a4c1daf3a"},
		{name: "weight write", tool: "health_log_weight", args: `{"kg":70.5}`, hash: "f485dc8af15acc0aff5208f53ee06df0ecf803db28a9845c35015eccb5143464"},
		{name: "bad weight", tool: "health_log_weight", args: `{"kg":401}`, hash: "3a024f00a7c2282d51b1c23a0c15cf4f1f26e393922a5b0f45372a03a8698622"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.HealthEnvironment(t)
			testdump.HealthCLIEnvironment(t, test.mode)
			root := testdump.HealthRoot(t)

			fs, err := device.NewDirFS(root)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := (health.ListActivitiesAction{Profile: fit.MustProfile()}).Run(t.Context(), watchsession.New(fs), true); err != nil {
				t.Fatal(err)
			}

			if err := fs.Close(); err != nil {
				t.Fatal(err)
			}

			client := newMCPContractClientWithOpener(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: json.RawMessage(test.args)})
			if err != nil {
				t.Fatal(err)
			}

			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}

			testdump.CheckDigest(t, testdump.NormalizeHealthDates(data), test.hash)
		})
	}
}
