package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPTransportToolCatalogsMatch(t *testing.T) { //nolint:paralleltest // both backends read isolated process account paths.
	httpClient := newContractClient(t)
	stdioClient := newMCPContractClient(t)

	httpTools, err := httpClient.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	stdioTools, err := stdioClient.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(httpTools.Tools) != 33 || len(stdioTools.Tools) != 33 {
		t.Fatalf("HTTP tools=%d stdio tools=%d want=33", len(httpTools.Tools), len(stdioTools.Tools))
	}

	httpJSON, err := json.Marshal(httpTools.Tools)
	if err != nil {
		t.Fatal(err)
	}

	stdioJSON, err := json.Marshal(stdioTools.Tools)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(httpJSON, stdioJSON) {
		t.Fatal("HTTP and stdio tool definitions differ")
	}
}

func newMCPContractClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	isolateContractAccount(t)

	return newMCPContractClientForRoot(t, testdump.Root(t))
}

func newMCPContractClientForRoot(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	return newMCPContractClientWithOpener(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })
}

// Only the I/O transport differs from ServeMCP. Account loading, runtime owners,
// feature registration and shutdown all use the production composition.
func newMCPContractClientWithOpener(t *testing.T, open watchsession.Opener) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 15*time.Second)
	ct, st := mcp.NewInMemoryTransports()
	done := make(chan struct{})

	var serveErr error

	go func() {
		defer close(done)

		serveErr = app.ServeMCPTransport(ctx, open, contractVersion, st)
	}()

	var client *mcp.ClientSession

	t.Cleanup(func() {
		defer cancel()

		if client != nil {
			if err := client.Close(); err != nil {
				t.Error(err)
			}
		}

		select {
		case <-done:
			if serveErr != nil {
				t.Errorf("production MCP backend shutdown: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			cancel()
			t.Error("production MCP backend did not join")
		}
	})

	var err error

	client, err = mcp.NewClient(&mcp.Implementation{Name: contractVersion, Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	if got := client.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Fatalf("production stdio composition MCP protocol=%q want 2026-07-28", got)
	}

	return client
}
