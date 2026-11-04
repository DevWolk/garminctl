package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	appsmcp "github.com/DevWolk/garminctl/internal/containers/apps/ui/mcp"
	settingsmcp "github.com/DevWolk/garminctl/internal/containers/settings/ui/mcp"
	"github.com/DevWolk/garminctl/internal/containers/store"
	storemcp "github.com/DevWolk/garminctl/internal/containers/store/ui/mcp"
	watchmcp "github.com/DevWolk/garminctl/internal/containers/watch/ui/mcp"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestWithWatchDropsOnlyDisconnectedSessions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		drop bool
	}{
		{name: "disconnected", err: device.ErrDisconnected, drop: true},
		{name: "busy", err: device.ErrBusy},
		{name: "cancelled before I/O", err: context.Canceled},
		{name: "domain error mentioning bridge", err: errors.New("invalid bridge label")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			opens := 0
			lazy := garmin.NewLazy(func(context.Context) (device.Device, error) {
				opens++
				return device.NewDirFS(root)
			})
			t.Cleanup(lazy.Drop)

			s := &server{svc: lazy}

			handler := withWatch(s, func(context.Context, *garmin.Service, struct{}) (struct{}, error) { return struct{}{}, tt.err })
			if _, _, err := handler(t.Context(), nil, struct{}{}); !errors.Is(err, tt.err) {
				t.Fatalf("MCP changed the tool error: %v", err)
			}

			if _, err := lazy.Get(t.Context()); err != nil {
				t.Fatal(err)
			}

			wantOpens := 1
			if tt.drop {
				wantOpens = 2
			}

			if opens != wantOpens {
				t.Fatalf("connection opens=%d, want=%d", opens, wantOpens)
			}
		})
	}
}

// newClient connects an MCP client to a server that serves a dump built
// from the garmin and fit fixtures.
func newClient(t *testing.T) *mcp.ClientSession {
	t.Helper()

	return newClientForRoot(t, testdump.Root(t))
}

func newClientForRoot(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()

	return newClientWithOpener(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })
}

func newClientWithOpener(t *testing.T, open garmin.Opener) *mcp.ClientSession {
	t.Helper()

	runtime := garmin.NewRuntime(t.Context(), open)
	s := &server{svc: runtime.Watch, runtime: runtime}
	srv := newServer(s, "test", gc.New(filepath.Join(t.TempDir(), "garmin-connect.json")),
		&settingsmcp.Tools{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile(), BindContext: runtime.RequestContext},
		&watchmcp.Tools{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile(), BindContext: runtime.RequestContext, AppCount: func(ctx context.Context, sess *watchsession.Session) (int, error) {
			apps, err := garmin.NewFromSession(sess).Apps(ctx)
			return len(apps), err
		}}, &appsmcp.Tools{Manager: runtime.Watch.Manager(), CacheFor: runtime.Watch.AppSchemaCacheFor, BindContext: runtime.RequestContext},
		&storemcp.Tools{Manager: runtime.Watch.Manager(), BindContext: runtime.RequestContext, Registry: func(sess *watchsession.Session) store.InstalledApps {
			if sess == nil {
				return nil
			}

			return garmin.NewFromSession(sess).Apps
		}})

	ct, st := mcp.NewInMemoryTransports()

	ctx := context.Background()

	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()

		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 3*time.Second)
		defer cancel()

		if err := runtime.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})

	return cs
}

func TestNewServer_ToolContract(t *testing.T) {
	t.Parallel()

	cs := newClient(t)

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

func TestTools_OverDump(t *testing.T) {
	t.Parallel()

	cs := newClient(t)

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
		{name: "watch_list_files", args: map[string]any{"path": "GARMIN"}, check: func(t *testing.T, out map[string]any) {
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
		{name: "music_list"},
		{name: "watch_decode_fit", args: map[string]any{"path": "GARMIN/Settings/Settings.fit"}},
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
