package app

import (
	"context"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const (
	appSettingsTool = "watch_app_settings"
	appArgument     = "app"
	appSchemaTool   = "watch_app_settings_schema"
	contractMissing = "missing"
)

func TestAppsMCPContracts(t *testing.T) { //nolint:paralleltest // isolates process schema client and cache environment
	testdump.AppEnvironment(t)

	for _, test := range []struct { //nolint:paralleltest // cases share the process schema client and cache environment
		name, tool string
		args       map[string]any
		hash       string
	}{
		{name: "apps list", tool: "watch_apps", args: map[string]any{}, hash: "ee2b85f5f81c4c66b78fa768a53424c480d754eb1c14de331ebe4e0f920b5e75"},
		{name: "settings", tool: appSettingsTool, args: map[string]any{appArgument: testdump.AppRef}, hash: "0559a5f50a7414d9ed79faf7f9f11d1cddc2dd3d78de790a58103674989b2f29"},
		{name: "apps schema", tool: appSchemaTool, args: map[string]any{appArgument: testdump.AppRef}, hash: "dcb789f0cc5d6215fb47e451638e0c12ca98b918dcf3c4d703fc9766f2f52c49"},
		{name: "one key schema", tool: appSchemaTool, args: map[string]any{appArgument: testdump.AppRef, "key": "Mode"}, hash: "cda1dbe81a4704d4e2e8aa4c5a934bf5878af2ef5dd6a25dd4dddacd1d70dd6a"},
		{name: "set", tool: "watch_set_app_settings", args: map[string]any{appArgument: testdump.AppRef, "values": map[string]any{"Mode": "Night"}}, hash: "99cb608af2b8ee5bf6bc2877f7ae33b8b5aaad4e9cb27e563c48d7362ddd1970"},
		{name: "missing key", tool: appSchemaTool, args: map[string]any{appArgument: testdump.AppRef, "key": contractMissing}, hash: "e356cef104f1d80601d9ee5ba30814ccf1e283db9863ddf0191345588f5f2aea"},
		{name: "missing app", tool: appSettingsTool, args: map[string]any{appArgument: contractMissing}, hash: "191996b780d27504ce14eaab42db8a7c056fb27844a0c238fe8d96d81492d406"},
		{name: "read only", tool: "watch_set_app_settings", args: map[string]any{appArgument: testdump.AppRef, "values": map[string]any{"Locked": 2}}, hash: "66fac48d0767c9e6728054c06c94853958ec30eca6b9fdd0a345c3593e1e2535"},
		{name: "hide missing", tool: "watch_set_app_hidden", args: map[string]any{appArgument: contractMissing, "hidden": true}, hash: "f33cbd5bda52d80c4982487d9898c1253f0ee81a8d07bdb82ead297911c06ecd"},
		{name: "remove", tool: "watch_remove_app", args: map[string]any{appArgument: testdump.AppRef}, hash: "c2bdb38a9aece042b73fabe30ab4f9c3d7fa4363ff0fb8b1093078786e557d78"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dev, _ := testdump.Apps(t)
			isolateContractAccount(t)
			client := newMCPContractClientWithOpener(t, func(context.Context) (device.Device, error) { return dev, nil })

			checkMCPResultContract(t, client, test.tool, test.args, test.hash)
		})
	}
}

func TestAppsMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	client := newMCPContractClient(t)

	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"watch_apps":             "08a6c28d92351ed83f1a152adcbfe1e736eee8b48dd366f33035a4f66bb6e062",
		appSettingsTool:          "9b5d1116937682b6ea685c283086aa68aacae023ebf2aa05c66a83687dccd04b",
		appSchemaTool:            "7ff45b6a51cd5cc5bf2bceb4d5f0697f8c4e0ab5c69d27290c9ca3f59ce245b8",
		"watch_set_app_settings": "539b315bcd9ed2d836153138d52219c4de9a7c7688bbec1adf9c1c60f1fa0b28",
		"watch_set_app_hidden":   "8b413887f68619c1b29360009c14481905b8e5d13e569c2b5e1e97df97c0ce5a",
		"watch_remove_app":       "865320785b06e7a9bf454f2e3bd1f93e8754c7b495ea6d8c210088577162c941",
	}
	seen := 0

	for _, tool := range tools.Tools {
		if hash, ok := want[tool.Name]; ok {
			seen++

			t.Run(tool.Name, func(t *testing.T) { t.Parallel(); testdump.CheckJSON(t, tool, hash) })
		}
	}

	if seen != len(want) {
		t.Fatalf("registered app tools=%d want=%d", seen, len(want))
	}
}
