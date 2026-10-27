package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
)

const historyLimit = 100

// Client records session metadata; an open session may be idle.
type Client struct {
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	ConnectedAt   time.Time `json:"connectedAt"`
	LastSeen      time.Time `json:"lastSeen"`
	SessionActive bool      `json:"sessionActive"`
}

// Activity records a tool invocation without its arguments or response data.
type Activity struct {
	Client     string    `json:"client"`
	Tool       string    `json:"tool"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMS int64     `json:"durationMs"`
	Running    bool      `json:"running"`
	Failed     bool      `json:"failed"`
}

// Snapshot is a bounded, in-memory view of MCP sessions and recent calls.
type Snapshot struct {
	Clients  []Client   `json:"clients"`
	Activity []Activity `json:"activity"`
}

// HTTPServer shares the web UI's watch and cloud session over Streamable HTTP.
// Its caller must apply the same Host and Origin checks as the local API.
type HTTPServer struct {
	ctx       context.Context //nolint:containedctx // This protocol owner cancels its request lifetime at Close and joins its worker.
	cancel    context.CancelFunc
	server    *mcp.Server
	handler   http.Handler
	mu        sync.Mutex
	clients   map[*mcp.ServerSession]Client
	activity  []*Activity
	stopping  bool
	requests  int
	drained   chan struct{}
	closeDone chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// NewHTTPServer registers tools and tracks sessions and calls for the web UI.
func NewHTTPServer(runtime *garmin.Runtime, cloudClient *gc.Client, version string) *HTTPServer {
	ctx, cancel := context.WithCancel(runtime.Context())
	h := &HTTPServer{ctx: ctx, cancel: cancel, server: newServer(&server{svc: runtime.Watch, runtime: runtime}, version, cloudClient), clients: make(map[*mcp.ServerSession]Client), drained: make(chan struct{}), closeDone: make(chan struct{})}
	h.server.AddReceivingMiddleware(h.observe)
	h.handler = mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return h.server }, &mcp.StreamableHTTPOptions{
		JSONResponse:   true,
		SessionTimeout: 30 * time.Minute,
	})

	return h
}

// ServeHTTP handles an MCP request after the caller's origin checks.
func (h *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		http.Error(w, "application is stopping", http.StatusServiceUnavailable)

		return
	}

	h.requests++

	h.mu.Unlock()
	defer func() {
		h.mu.Lock()

		h.requests--
		if h.stopping && h.requests == 0 {
			close(h.drained)
		}
		h.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(r.Context())

	stop := context.AfterFunc(h.ctx, cancel)
	defer func() { stop(); cancel() }()

	if h.ctx.Err() != nil {
		cancel()
	}

	h.handler.ServeHTTP(w, r.WithContext(ctx))
}

// Close stops protocol admission, waits for active HTTP requests and closes all
// SDK sessions. The runtime owner cancels tool contexts before calling Close.
// A timed-out caller can rejoin the same owned cleanup worker on a later call.
func (h *HTTPServer) Close(ctx context.Context) error {
	h.closeOnce.Do(func() {
		h.mu.Lock()

		h.stopping = true
		if h.requests == 0 {
			close(h.drained)
		}

		h.mu.Unlock()

		h.cancel()
		go func() {
			defer close(h.closeDone)

			<-h.drained

			for session := range h.server.Sessions() {
				h.closeErr = errors.Join(h.closeErr, session.Close())
			}
		}()
	})

	select {
	case <-h.closeDone:
		return h.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *HTTPServer) observe(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		session, ok := req.GetSession().(*mcp.ServerSession)
		if !ok {
			return next(ctx, method, req)
		}

		h.touch(session)

		var call *Activity
		if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && method == "tools/call" {
			call = h.beginCall(session, params.Name)
		}

		result, err := next(ctx, method, req)

		h.touch(session)

		if call != nil {
			toolResult, _ := result.(*mcp.CallToolResult)

			h.mu.Lock()
			call.Running = false
			call.DurationMS = time.Since(call.StartedAt).Milliseconds()
			call.Failed = err != nil || (toolResult != nil && toolResult.IsError)
			h.mu.Unlock()
		}

		return result, err
	}
}

func (h *HTTPServer) touch(session *mcp.ServerSession) {
	params := session.InitializeParams()
	if params == nil || params.ClientInfo == nil {
		return // Protocol discovery can use a temporary session without a client.
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	client, exists := h.clients[session]
	if !exists {
		if len(h.clients) >= historyLimit {
			h.evictOldestClient()
		}

		client.ConnectedAt = time.Now()
		client.Name = "Unknown client"
	}

	client.Name, client.Version = params.ClientInfo.Name, params.ClientInfo.Version
	client.LastSeen = time.Now()
	h.clients[session] = client
}

func (h *HTTPServer) evictOldestClient() {
	var oldest *mcp.ServerSession
	for session, client := range h.clients {
		if oldest == nil || client.LastSeen.Before(h.clients[oldest].LastSeen) {
			oldest = session
		}
	}

	delete(h.clients, oldest)
}

func (h *HTTPServer) beginCall(session *mcp.ServerSession, name string) *Activity {
	h.mu.Lock()
	defer h.mu.Unlock()

	call := &Activity{Client: h.clients[session].Name, Tool: name, StartedAt: time.Now(), Running: true}

	h.activity = append(h.activity, call)
	if len(h.activity) > historyLimit {
		h.activity = h.activity[1:]
	}

	return call
}

// Snapshot copies session metadata and recent calls. SessionActive means the
// SDK session is open; LastSeen records its latest request.
func (h *HTTPServer) Snapshot() Snapshot {
	active := make(map[*mcp.ServerSession]bool)
	for session := range h.server.Sessions() {
		active[session] = true
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	out := Snapshot{Clients: make([]Client, 0, len(h.clients)), Activity: make([]Activity, 0, len(h.activity))}
	for session, client := range h.clients {
		client.SessionActive = active[session]
		out.Clients = append(out.Clients, client)
	}

	slices.SortFunc(out.Clients, func(a, b Client) int { return b.LastSeen.Compare(a.LastSeen) })

	for _, call := range slices.Backward(h.activity) {
		out.Activity = append(out.Activity, *call)
	}

	return out
}
