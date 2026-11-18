package contracts

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

type rootConnectTransport struct{ local, account http.RoundTripper }

func (tr rootConnectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() == "127.0.0.1" {
		return tr.local.RoundTrip(req)
	}

	return tr.account.RoundTrip(req)
}

//nolint:paralleltest // ConnectCLIEnvironment isolates process-global account paths and HTTP transport.
func TestServeListenerConnectSharesAccountWatchAndSync(t *testing.T) {
	local := http.DefaultTransport

	testdump.ConnectCLIEnvironment(t, "")

	http.DefaultTransport = rootConnectTransport{local: local, account: http.DefaultTransport}
	root := testdump.ConnectRoot(t)

	var opens, closes atomic.Int32

	base, stop := serveConnectRoot(t, root, &opens, &closes)

	var status connect.CloudStatus
	rootGetJSON(t.Context(), t, base, "/api/cloud/status", &status)

	if !status.LoggedIn || status.Registered || opens.Load() != 0 {
		t.Fatalf("account-only status=%+v opens=%d", status, opens.Load())
	}

	var settings []connect.CloudSetting
	rootGetJSON(t.Context(), t, base, "/api/cloud/settings", &settings)

	session, err := mcp.NewClient(&mcp.Implementation{Name: "root-connect", Version: contractVersion}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	var toolSettings struct {
		Settings []connect.CloudSetting `json:"settings"`
	}
	rootConnectTool(t, session, "cloud_settings", map[string]any{}, &toolSettings)

	if len(settings) == 0 || !reflect.DeepEqual(settings, toolSettings.Settings) {
		t.Fatal("HTTP and MCP Connect settings differ")
	}

	var result connect.SyncResult
	rootConnectTool(t, session, "cloud_sync", map[string]any{}, &result)
	checkRootConnectSync(t, base, &result)

	if opens.Load() != 1 || closes.Load() != 0 {
		t.Fatalf("Connect owners opens=%d closes=%d", opens.Load(), closes.Load())
	}

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	stop()

	if closes.Load() != 1 {
		t.Fatalf("Connect watch closed %d times", closes.Load())
	}
}

func checkRootConnectSync(t *testing.T, base string, result *connect.SyncResult) {
	t.Helper()

	var state struct {
		Running bool                `json:"running"`
		Result  *connect.SyncResult `json:"result"`
		Error   string              `json:"error"`
	}
	rootGetJSON(t.Context(), t, base, "/api/cloud/sync", &state)

	if state.Running || state.Error != "" || !reflect.DeepEqual(state.Result, result) {
		t.Fatalf("HTTP did not observe MCP's completed sync: %+v", state)
	}

	if result.Upload == nil || result.Upload.Uploaded != 1 || len(result.Pulled) != 1 || len(result.Errors) != 0 || result.Pulled[0].Error != "" {
		t.Fatalf("Connect result=%+v", result)
	}

	var status connect.CloudStatus
	rootGetJSON(t.Context(), t, base, "/api/cloud/status", &status)

	if !status.LoggedIn || !status.Registered {
		t.Fatalf("shared open-watch status=%+v", status)
	}
}

func rootConnectTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}

	if result.IsError {
		t.Fatalf("%s failed: %+v", name, result)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func serveConnectRoot(t *testing.T, root string, opens, closes *atomic.Int32) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)

	var config net.ListenConfig

	listener, err := config.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	done := make(chan struct{})

	var serveErr error

	go func() {
		defer close(done)

		serveErr = app.ServeListener(ctx, listener, func(context.Context) (device.Device, error) {
			opens.Add(1)

			dev, err := device.NewDirFS(root)

			return trackedRootDevice{Device: dev, closes: closes, settingsReads: new(atomic.Int32)}, err
		}, "", false, "connect-root-test")
	}()

	stop := sync.OnceFunc(func() {
		cancel()

		select {
		case <-done:
			if serveErr != nil {
				t.Errorf("Connect root shutdown: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("Connect root did not finish shutdown")
		}
	})
	t.Cleanup(stop)

	return "http://" + listener.Addr().String(), stop
}
