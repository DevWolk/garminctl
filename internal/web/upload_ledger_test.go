package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/paths"
)

type ledgerFaultTransport struct {
	path  string
	calls atomic.Int32
}

func (tr *ledgerFaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.URL.Path != "/upload-service/upload/activity" {
		return nil, fmt.Errorf("unexpected ledger fault request: %s %s", req.Method, req.URL.Path)
	}

	tr.calls.Add(1)
	// The remote side accepts the file, then the local destination becomes
	// unusable before the caller can persist its upload receipt.
	if err := os.Mkdir(tr.path, 0o700); err != nil {
		return nil, err
	}

	return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
}

func TestLedgerSaveFailureVisibleThroughMCPAndHTTPState(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)

	if err := os.MkdirAll(paths.Cache(), 0o700); err != nil {
		t.Fatal(err)
	}

	client, _ := signedInSyncClient(t)
	transport := &ledgerFaultTransport{path: filepath.Join(paths.Cache(), "uploaded-42.json")}
	client.HTTP = &http.Client{Transport: transport}
	_, ts, root := newTestBackend(t, client)

	const xml = `<Device><Id>42</Id><MassStorageMode><DataType><Name>FIT_TYPE_4</Name><File><Location><Path>GARMIN/LedgerTest</Path><FileExtension>fit</FileExtension></Location><TransferDirection>OutputFromUnit</TransferDirection></File></DataType></MassStorageMode></Device>`
	if err := os.WriteFile(filepath.Join(root, "GARMIN", "GarminDevice.xml"), []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(root, "GARMIN", "LedgerTest"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "GARMIN", "LedgerTest", "sample.fit"), []byte("synthetic upload"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "ledger-test", Version: "1"}, nil)

	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "cloud_sync", Arguments: map[string]any{"upload": true, "download": false}})
	if err != nil || result.IsError {
		t.Fatalf("MCP partial result replaced by tool error: %+v, %v", result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var structured connect.SyncResult
	if err := json.Unmarshal(data, &structured); err != nil {
		t.Fatal(err)
	}

	checkLedgerPartialSync(t, &structured)

	var state struct {
		Running bool                `json:"running"`
		Error   string              `json:"error"`
		Result  *connect.SyncResult `json:"result"`
	}
	if status := getJSON(t, ts, "/api/cloud/sync", &state); status != http.StatusOK {
		t.Fatalf("HTTP sync state status=%d", status)
	}

	if state.Running || state.Error != "" || transport.calls.Load() != 1 {
		t.Fatalf("state running=%t outer error=%q upload calls=%d", state.Running, state.Error, transport.calls.Load())
	}

	checkLedgerPartialSync(t, state.Result)
}

func checkLedgerPartialSync(t *testing.T, result *connect.SyncResult) {
	t.Helper()

	if result == nil || result.Upload == nil || result.Upload.Uploaded != 1 {
		t.Fatalf("accepted upload missing: %+v", result)
	}

	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "upload: save upload ledger") {
		t.Fatalf("ledger error missing: %+v", result)
	}
}
