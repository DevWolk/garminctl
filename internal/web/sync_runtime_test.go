package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
)

type noSyncNetwork struct{ calls atomic.Int32 }

func (n *noSyncNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.calls.Add(1)
	return nil, errors.New("unexpected synthetic account network call")
}

func signedInSyncClient(t *testing.T) (*gc.Client, *noSyncNetwork) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "account.json")

	data, err := json.Marshal(gc.Session{Email: "sync@example.invalid", OAuth1: &gc.OAuth1Token{Token: "synthetic", Domain: gc.DefaultDomain}, OAuth2: &gc.OAuth2Token{AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).Unix()}})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}

	client := gc.New(file)
	if err := client.StoreError(); err != nil {
		t.Fatal(err)
	}

	transport := new(noSyncNetwork)
	client.HTTP = &http.Client{Transport: transport}

	return client, transport
}

func TestHTTPSyncConflictsAndMCPWaiterSharesRuntimeJob(t *testing.T) {
	t.Parallel()
	client, transport := signedInSyncClient(t)
	s, ts, _ := newTestBackend(t, client)

	lease, err := s.svc.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	gate := make(chan struct{})

	release := sync.OnceFunc(func() { close(gate) })
	defer release()

	job, _, err := s.runtime.Sync.Start(t.Context(), garmin.SyncOptions{Upload: true, Download: true, Epoch: epoch}, func(ctx context.Context, _ garmin.Progress) (*garmin.SyncResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-gate:
			return &garmin.SyncResult{Errors: []string{"synthetic result"}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/api/cloud/sync", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusConflict {
		t.Fatalf("HTTP did not reject duplicate sync: %d", response.StatusCode)
	}

	checkMCPWaiterCancellation(t, s, ts.URL)

	if !s.runtime.Sync.Snapshot().Running || transport.calls.Load() != 0 {
		t.Fatalf("MCP started independent sync or cancelled shared job: network calls=%d", transport.calls.Load())
	}

	release()

	result, err := job.Wait(t.Context())
	if err != nil || len(result.Errors) != 1 {
		t.Fatalf("original result lost: %+v, %v", result, err)
	}
}

func checkMCPWaiterCancellation(t *testing.T, s *Server, url string) {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "sync-test", Version: "1"}, nil)

	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: url + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "cloud_sync", Arguments: map[string]any{}})
		done <- err
	}()

	waitMCPActivity(t, s, true)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("MCP waiter cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MCP waiter did not cancel")
	}

	waitMCPActivity(t, s, false)
}

func waitMCPActivity(t *testing.T, s *Server, running bool) {
	t.Helper()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for {
		activity := s.mcp.Snapshot().Activity
		if len(activity) == 1 && activity[0].Running == running {
			return
		}

		select {
		case <-deadline.C:
			t.Fatalf("MCP activity did not reach running=%t", running)
		case <-tick.C:
		}
	}
}

func TestHTTPStartsRuntimeSyncAndKeepsSnapshotShape(t *testing.T) {
	t.Parallel()
	client, transport := signedInSyncClient(t)
	_, ts, _ := newTestBackend(t, client)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/api/cloud/sync", strings.NewReader(`{"upload":false,"download":false}`))
	if err != nil {
		t.Fatal(err)
	}

	response, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("sync start status=%d", response.StatusCode)
	}

	var state map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"running", "startedAt", "endedAt", "log", "result", "error"} {
		if _, ok := state[key]; !ok {
			t.Errorf("sync state lost key %q", key)
		}
	}

	if transport.calls.Load() != 0 {
		t.Fatal("disabled directions performed network sync")
	}
}
