package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const healthCommandName = "health"

func TestHealthCLIContracts(t *testing.T) { //nolint:paralleltest // isolates account/cache paths, local zone and HTTP transport
	previous := time.Local
	time.Local = time.UTC

	t.Cleanup(func() { time.Local = previous })

	for _, test := range []struct { //nolint:paralleltest // cases isolate process state
		name, cmd, mode string
		args            []string
		json            bool
		code            int
		hash            string
	}{
		{name: "activity text", cmd: "activity", args: []string{"fixture.fit"}, hash: "6a51a5dd7dc2ddf3899e6179286bc8595e706ac03838834baa454927f2770aa0"},
		{name: "activity JSON", cmd: "activity", args: []string{"fixture.fit"}, json: true, hash: "e0e53c559592f632f35d295cdae3cd0a41f2cf283daf0f9c2b4cb60cc3a29989"},
		{name: "activities text", cmd: "activities", args: []string{"-wait", "-n", "1"}, hash: "1596c53f2602555711bd02b6e807cf7ff5a4447e09a7aa221748695a5abea270"},
		{name: "activities JSON", cmd: "activities", args: []string{"-wait", "-n", "1"}, json: true, hash: "c4dce382de2d86ba1cd3870a205f3f784625bf799d4fcbc6421c23ab3bfc58e3"},
		{name: "daily table", cmd: healthCommandName, args: []string{"-days", "2"}, hash: "43db70e947109931bcc6d546686302f5661b65737a74c73fd33820805f8c75d4"},
		{name: "daily JSON", cmd: healthCommandName, args: []string{"-days", "2"}, json: true, hash: "58caeb07e494e5c656b75f79facc091909fe186003ce9b6be04237fd3b9f1fb6"},
		{name: "long daily JSON", cmd: healthCommandName, args: []string{"-days", "15"}, json: true, hash: "ed604593895880ff5f9214ecff6e3a1f31f12f8eabe462db799b597ffec0f138"},
		{name: "history table", cmd: healthCommandName, args: []string{"history", "3"}, hash: "27f8d4c4a21a31353341302405f2e639a572c36db3fb361dd62860bfbe7f8238"},
		{name: "intraday text", cmd: healthCommandName, args: []string{"day", "2000-01-02"}, hash: "9f30250d4d8896ced6d62644ce29979f0ea77723ca7683d717e5b94b5280d6a0"},
		{name: "extras text", cmd: healthCommandName, args: []string{"extras"}, hash: "72000dc02d42d71c5629d69d067c3dd8b18bcc70fa3be77b106fef40a8122706"},
		{name: "partial extras JSON", cmd: healthCommandName, args: []string{"extras"}, mode: "partial extras", json: true, hash: "b5f05ddbd500cf1efac86275d77227fb3d364cf6b09978f3f08bfb837730d16e"},
		{name: "weight text", cmd: healthCommandName, args: []string{"weight", "70.5"}, hash: "aaef5ae8d5d679921303b965d048e23fe3965a9c7965afd9b492de2bb0507c4a"},
		{name: "weight JSON flag", cmd: healthCommandName, args: []string{"weight", "70.5"}, json: true, hash: "aaef5ae8d5d679921303b965d048e23fe3965a9c7965afd9b492de2bb0507c4a"},
		{name: "invalid weight value", cmd: healthCommandName, args: []string{"weight", "wrong"}, code: 1, hash: "ef708fecc446ea124d47ed43e5520acd2e565f4f5a8500d0a73e94b285e04a4b"},
		{name: "invalid history days", cmd: healthCommandName, args: []string{"history", "0"}, code: 2, hash: "b06281670f17461b87a6f54663455e8a39d932ea29b738afdff6ef2cf48a9c0e"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.HealthEnvironment(t)
			testdump.HealthCLIEnvironment(t, test.mode)
			root := testdump.HealthRoot(t)

			argv := []string{rootFlag, root}
			if test.json {
				argv = append(argv, "-json")
			}

			argv = append(append(argv, test.cmd), test.args...)

			var output, diagnostics bytes.Buffer

			err := run(argv, &output, &diagnostics)
			if exitCode(err) != test.code || diagnostics.Len() != 0 {
				t.Fatalf("code=%d error=%v diagnostics=%s", exitCode(err), err, &diagnostics)
			}

			data := output.Bytes()
			if err != nil {
				data = append(data, []byte("error: "+err.Error())...)
			}

			testdump.CheckDigest(t, testdump.NormalizeHealthDates(data), test.hash)
		})
	}
}
