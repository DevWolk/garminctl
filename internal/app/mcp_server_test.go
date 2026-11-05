package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewServer_ToolContract(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	cs := newMCPContractClient(t)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	const want = 33
	if len(res.Tools) != want {
		t.Fatalf("%d tools registered, want %d", len(res.Tools), want)
	}

	titles := map[string]string{}

	for _, tool := range res.Tools {
		if tool.Annotations == nil || tool.Annotations.Title == "" {
			t.Errorf("%s: no title annotation", tool.Name)
			continue
		}

		if other, dup := titles[tool.Annotations.Title]; dup {
			t.Errorf("%s and %s share the title %q", tool.Name, other, tool.Annotations.Title)
		}

		titles[tool.Annotations.Title] = tool.Name

		if tool.Annotations.OpenWorldHint == nil {
			t.Errorf("%s: open-world hint not declared", tool.Name)
		}

		if !tool.Annotations.ReadOnlyHint && tool.Annotations.DestructiveHint == nil {
			t.Errorf("%s: a writing tool must say whether it is destructive", tool.Name)
		}

		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema", tool.Name)
		}

		if !strings.Contains(tool.Name, "_") {
			t.Errorf("%s: name has no module prefix", tool.Name)
		}
	}
}

func TestTools_OverDump(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	cs := newMCPContractClient(t)

	tests := []struct {
		name  string
		args  map[string]any
		check func(t *testing.T, out map[string]any)
	}{
		{name: "watch_info", check: func(t *testing.T, out map[string]any) {
			t.Helper()

			if len(out) == 0 {
				t.Fatal("empty summary")
			}
		}},
		{name: "watch_list_files", args: map[string]any{watchPathKey: "GARMIN"}, check: func(t *testing.T, out map[string]any) {
			t.Helper()

			var files struct {
				Entries []device.Entry `json:"entries"`
			}
			if b, err := json.Marshal(out); err == nil {
				_ = json.Unmarshal(b, &files)
			}

			if !slices.ContainsFunc(files.Entries, func(e device.Entry) bool { return e.Name == "GarminDevice.xml" }) {
				t.Fatalf("GARMIN listing = %+v", files.Entries)
			}
		}},
		{name: "watch_settings", args: map[string]any{"message": "deviceSettings"}},
		{name: "watch_settings_schema", args: map[string]any{"message": "deviceSettings"}},
		{name: "watch_apps"},
		{name: musicListTool},
		{name: "watch_decode_fit", args: map[string]any{watchPathKey: "GARMIN/Settings/Settings.fit"}},
		{name: "cloud_status", check: func(t *testing.T, out map[string]any) {
			t.Helper()

			if out["loggedIn"] != false {
				t.Fatalf("cloud_status without a session = %v", out)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tt.name, Arguments: tt.args})
			if err != nil {
				t.Fatal(err)
			}

			if res.IsError {
				t.Fatalf("tool error: %v", res.Content)
			}

			if res.StructuredContent == nil {
				t.Fatal("no structured content")
			}

			if tt.check == nil {
				return
			}

			var out map[string]any
			if b, err := json.Marshal(res.StructuredContent); err == nil {
				_ = json.Unmarshal(b, &out)
			}

			tt.check(t, out)
		})
	}
}
