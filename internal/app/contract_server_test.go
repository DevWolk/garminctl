package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const (
	contractVersion          = "test"
	contractOKHash           = "f9e952a6078dd131bab1fc9c7c94a923ee48bc2d0b2ebb670349397efcf5e1bb"
	contractTrailingJSONHash = "a91149e68d1e9075873e9379fce36cec342265fe2ce7993a85c1e0bd2bfee77f"
)

// contractServer drives the production composition over a real listener.
// It never constructs feature registrars or its own account/runtime owners.
type contractServer struct {
	URL    string
	client *http.Client
}

func (s *contractServer) Client() *http.Client { return s.client }

func checkHTTPContract(t *testing.T, s *contractServer, method, path, body string, status int, hash string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	response, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("status=%d body=%s error=%v", response.StatusCode, data, err)
	}

	testdump.CheckDigest(t, data, hash)
}

func connectContractWatch(t *testing.T, s *contractServer) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL+"/api/connect", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("connect fixture status=%d", response.StatusCode)
	}
}

func isolateContractAccount(t *testing.T) {
	t.Helper()

	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)
}

func serveContractRoot(t *testing.T, open watchsession.Opener) *contractServer {
	t.Helper()

	// Protocol clients close before the backend cleanup. Their test context is
	// already cancelled when Cleanup runs, so the backend owns a bounded lifetime.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 15*time.Second)

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

		serveErr = ServeListener(ctx, listener, open, false, contractVersion)
	}()

	client := &http.Client{Transport: new(http.Transport), Timeout: 10 * time.Second}

	t.Cleanup(func() {
		client.CloseIdleConnections()
		cancel()

		select {
		case <-done:
			if serveErr != nil {
				t.Errorf("production contract backend shutdown: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("production contract backend did not join")
		}
	})

	return &contractServer{URL: "http://" + listener.Addr().String(), client: client}
}

func newContractClient(t *testing.T) *mcp.ClientSession {
	t.Helper()

	root := testdump.Root(t)

	return newContractClientWithOpener(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })
}

func newContractClientWithOpener(t *testing.T, open watchsession.Opener) *mcp.ClientSession {
	t.Helper()
	isolateContractAccount(t)

	server := serveContractRoot(t, open)

	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))

	client, err := mcp.NewClient(&mcp.Implementation{Name: contractVersion, Version: contractVersion}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: server.Client()}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		defer cancel()

		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})

	if got := client.InitializeResult().ProtocolVersion; got != "2025-11-25" {
		t.Fatalf("production HTTP MCP protocol=%q want 2025-11-25", got)
	}

	return client
}
