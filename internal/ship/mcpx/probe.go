package mcpx

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProbeServer initializes and pings a local server without invoking any tools.
// The caller validates the loopback endpoint and owns the outer startup deadline.
func ProbeServer(ctx context.Context, endpoint, version string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	transport := &http.Transport{ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()

	client := &http.Client{Timeout: 3 * time.Second, Transport: probeTransport{transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	session, err := mcp.NewClient(&mcp.Implementation{Name: "garminctl-startup", Version: version}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: 64 << 10}, nil)
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, session.Close()) }()

	info := session.InitializeResult().ServerInfo
	if info == nil || info.Name != "garminctl" || info.Version != version {
		return errors.New("restarted MCP server version does not match the selected release")
	}

	return session.Ping(ctx, nil)
}

type probeTransport struct{ base *http.Transport }

func (t probeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err == nil {
		response.Body = http.MaxBytesReader(nil, response.Body, 64<<10)
	}

	return response, err
}
