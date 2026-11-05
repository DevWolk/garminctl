package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/containers/music"
	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/testdump"
	"github.com/DevWolk/garminctl/internal/web"
)

type trackedRootDevice struct {
	device.Device
	closes        *atomic.Int32
	settingsReads *atomic.Int32
}

type rootStoreTransport struct {
	delegate http.RoundTripper
	requests *atomic.Int32
}

func (t rootStoreTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return t.delegate.RoundTrip(req)
}

func (d trackedRootDevice) Close() error {
	d.closes.Add(1)
	return d.Device.Close()
}

func (d trackedRootDevice) Read(ctx context.Context, path string) ([]byte, error) {
	if path == "GARMIN/Settings/Settings.fit" {
		d.settingsReads.Add(1)
	}

	return d.Device.Read(ctx, path)
}

func rootGetJSON(ctx context.Context, t *testing.T, base, path string, output any) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d", path, response.StatusCode)
	}

	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		t.Fatal(err)
	}
}

func TestServeListenerKeepsOneLazyWatchAcrossHTTPAndMCP(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)
	testdump.AppEnvironment(t)

	fixture, root := testdump.Apps(t)
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}

	testdump.AddMusic(t, root)
	testdump.AddHealth(t, root)

	var opens, closes, settingsReads, storeRequests atomic.Int32

	previous := ciqstore.HTTPClient
	ciqstore.HTTPClient = &http.Client{Transport: rootStoreTransport{delegate: previous.Transport, requests: &storeRequests}}

	t.Cleanup(func() { ciqstore.HTTPClient = previous })

	open := func(context.Context) (device.Device, error) {
		opens.Add(1)

		dev, err := device.NewDirFS(root)

		return trackedRootDevice{Device: dev, closes: &closes, settingsReads: &settingsReads}, err
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var network net.ListenConfig

	listener, err := network.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	base := "http://" + listener.Addr().String()
	done := make(chan struct{})

	var serveErr error

	go func() {
		defer close(done)

		serveErr = ServeListener(ctx, listener, open, false, "root-test")
	}()

	stop := sync.OnceFunc(func() {
		cancel()

		select {
		case <-done:
			if serveErr != nil {
				t.Errorf("root shutdown: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("root did not join server shutdown")
		}
	})
	t.Cleanup(stop)

	var identity map[string]string
	rootGetJSON(ctx, t, base, "/api/server", &identity)

	if identity["version"] != "root-test" || opens.Load() != 0 {
		t.Fatalf("identity=%v lazy opens=%d", identity, opens.Load())
	}

	var entries []device.Entry
	rootGetJSON(ctx, t, base, "/api/files?path=GARMIN", &entries)

	var settingsRows struct {
		Rows []map[string]any `json:"rows"`
	}
	rootGetJSON(ctx, t, base, "/api/settings", &settingsRows)

	if len(settingsRows.Rows) == 0 {
		t.Fatal("composition root did not register the settings HTTP adapter")
	}

	var appSettings map[string]any
	rootGetJSON(ctx, t, base, "/api/apps/G5RG4154/settings", &appSettings)

	if appSettings["schemaSource"] != "fixture:app-schema" {
		t.Fatalf("composition root did not load the fixture app schema: %v", appSettings)
	}

	checkRootMCPReads(ctx, t, base, entries, settingsRows.Rows, appSettings)

	if storeRequests.Load() != 1 {
		t.Fatalf("HTTP/MCP did not share the app schema cache: metadata requests=%d", storeRequests.Load())
	}

	checkRootStore(ctx, t, base)
	checkRootUpdates(ctx, t, base)
	checkRootMusic(ctx, t, base)

	if settingsReads.Load() != 1 {
		t.Fatalf("HTTP/MCP did not share the settings raw cache: reads=%d", settingsReads.Load())
	}

	checkRootWorkouts(ctx, t, base, root)
	checkRootHealth(ctx, t, base)

	if opens.Load() != 1 || closes.Load() != 0 {
		t.Fatalf("HTTP/MCP ownership: opens=%d closes=%d", opens.Load(), closes.Load())
	}

	stop()

	if closes.Load() != 1 {
		t.Fatalf("owned watch closes=%d, want 1", closes.Load())
	}
}

func checkRootMusic(ctx context.Context, t *testing.T, base string) {
	t.Helper()

	var expected music.Library
	rootGetJSON(ctx, t, base, "/api/music", &expected)

	if len(expected.Tracks) != 3 {
		t.Fatalf("root music tracks=%d", len(expected.Tracks))
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "root-music", Version: "1"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: musicListTool, Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("music tool=%+v error=%v", result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var actual music.Library
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("HTTP/MCP music libraries differ")
	}
}

func checkRootUpdates(ctx context.Context, t *testing.T, base string) {
	t.Helper()

	previous := garmin.HTTPClient
	garmin.HTTPClient = testdump.UpdateEnvironment(t, "")
	t.Cleanup(func() { garmin.HTTPClient = previous })

	var expected struct {
		Software []updates.SoftwareUpdate `json:"software"`
		Apps     []updates.AppUpdate      `json:"apps"`
	}
	rootGetJSON(ctx, t, base, "/api/updates/software", &expected.Software)
	rootGetJSON(ctx, t, base, "/api/updates/apps", &expected.Apps)

	client := mcp.NewClient(&mcp.Implementation{Name: "root-updates", Version: "1"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "watch_check_updates", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("update tool=%+v error=%v", result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	actual := expected

	actual.Software, actual.Apps = nil, nil
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("HTTP/MCP update checks differ")
	}
}

func checkRootMCPReads(ctx context.Context, t *testing.T, base string, expected []device.Entry, settingsRows []map[string]any, appSettings map[string]any) {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "root-test", Version: "1"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "watch_list_files", Arguments: map[string]any{watchPathKey: "GARMIN"}})
	if err != nil || result.IsError {
		t.Fatalf("MCP list=%+v error=%v", result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var output struct {
		Entries []device.Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(output.Entries, expected) {
		t.Fatalf("MCP entries=%+v HTTP entries=%+v", output.Entries, expected)
	}

	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "watch_settings", Arguments: map[string]any{"includeUnknown": true}})
	if err != nil || result.IsError {
		t.Fatalf("MCP settings=%+v error=%v", result, err)
	}

	data, err = json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var rows struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(rows.Rows, settingsRows) {
		t.Fatal("MCP includeUnknown and HTTP settings rows differ")
	}

	checkRootMCPAppSettings(ctx, t, session, appSettings)
}

func checkRootMCPAppSettings(ctx context.Context, t *testing.T, session *mcp.ClientSession, expected map[string]any) {
	t.Helper()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: appSettingsTool, Arguments: map[string]any{"app": testdump.AppRef}})
	if err != nil || result.IsError {
		t.Fatalf("MCP app settings=%+v error=%v", result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var actual map[string]any
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("MCP and HTTP app settings differ")
	}
}

func TestServeListenerClosesRuntimeOnListenerFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var network net.ListenConfig

	listener, err := network.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	// Supply an explicit in-memory runtime/account to test the shell's transfer
	// of ownership without consulting any default account store.
	runtime := NewRuntime(ctx, nil)

	account := gc.New(filepath.Join(t.TempDir(), "account.json"))
	if err := web.ServeRuntime(listener, runtime, account, false, contractVersion, web.Features{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed listener error=%v", err)
	}

	if runtime.Context().Err() == nil {
		t.Fatal("listener error retained an active runtime")
	}
}

func checkRootStore(ctx context.Context, t *testing.T, base string) {
	t.Helper()
	fixture := testdump.StoreEnvironment(t, "")

	var expected map[string]any
	rootGetJSON(ctx, t, base, "/api/store", &expected)
	fixture.CheckPage(t, 24, 0)

	client := mcp.NewClient(&mcp.Implementation{Name: "root-store", Version: "1"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "store_search", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("store tool=%+v error=%v", result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var actual map[string]any
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("HTTP/MCP store views differ")
	}
}

func checkRootWorkouts(ctx context.Context, t *testing.T, base, root string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(root, "GARMIN", "NewFiles"), 0o750); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/workouts", bytes.NewBufferString(`{"spec":`+testdump.WorkoutSpecJSON+`,"local":true}`))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("workout HTTP status=%d error=%v", resp.StatusCode, err)
	}

	testdump.NormalizePendingPath(t, []byte(result.Path))

	session, err := mcp.NewClient(&mcp.Implementation{Name: "root-workouts", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()

	out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "workout_create", Arguments: json.RawMessage(`{"spec":` + testdump.WorkoutSpecJSON + `,"local":true}`)})
	if err != nil || out.IsError {
		t.Fatalf("workout MCP=%+v error=%v", out, err)
	}

	files, err := filepath.Glob(filepath.Join(root, "GARMIN", "NewFiles", "*.FIT"))
	if err != nil || len(files) != 2 {
		t.Fatalf("workout files=%v error=%v", files, err)
	}

	for _, file := range files {
		checkRootWorkoutFile(t, file)
	}
}

func checkRootWorkoutFile(t *testing.T, file string) {
	t.Helper()

	data, err := os.ReadFile(file) //nolint:gosec // matched only under the temporary root's NewFiles
	if err != nil {
		t.Fatal(err)
	}

	f, err := fit.Decode(data)
	if err != nil || len(f.MessagesByNum(26)) != 1 || len(f.MessagesByNum(27)) != 5 {
		t.Fatalf("root workout FIT: %v", err)
	}

	if f.MessagesByNum(0)[0].Value(0) != uint64(5) {
		t.Fatal("root workout file type changed")
	}
}

func checkRootHealth(ctx context.Context, t *testing.T, base string) {
	t.Helper()

	var list health.ActivityList
	rootGetJSON(ctx, t, base, "/api/health/activities?wait=1", &list)

	if list.Total != 2 || list.Pending != 0 || len(list.Activities) != 2 || list.Activities[0].Sport == "unreadable" {
		t.Fatalf("root activity list=%+v", list)
	}

	var detail health.ActivityDetail
	rootGetJSON(ctx, t, base, "/api/health/activities/"+testdump.HealthActivity, &detail)

	if len(detail.Points) != 6 || len(detail.Laps) != 1 {
		t.Fatalf("root activity detail=%+v", detail)
	}

	session, err := mcp.NewClient(&mcp.Implementation{Name: "root-health", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()

	checkRootHealthTool(ctx, t, session, "activities_list", map[string]any{}, &list)
	checkRootHealthTool(ctx, t, session, "activity_detail", map[string]any{"file": testdump.HealthActivity}, &detail)
}

func checkRootHealthTool(ctx context.Context, t *testing.T, session *mcp.ClientSession, tool string, args map[string]any, want any) {
	t.Helper()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("health tool %s=%+v error=%v", tool, result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	var gotValue, wantValue any
	if err := json.Unmarshal(data, &gotValue); err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(expected, &wantValue); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("HTTP/MCP differ for %s", tool)
	}
}
