// Package web serves the browser UI and the JSON API.
package web

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/mcpserver"
	"github.com/DevWolk/garminctl/internal/notify"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

//go:embed ui
var uiFS embed.FS

const (
	shutdownTimeout = 3 * time.Second
	noteKey         = "note" // human-readable remark on a result object
)

// Features contains adapters assembled by the composition root. HTTP and MCP
// shells receive registrations, never import the feature implementations.
type Features struct {
	HTTP       []httpx.RouteRegistrar
	MCP        []mcpx.ToolRegistrar
	SyncRunner func(watchsession.ConnectionEpoch, bool, bool) garmin.SyncRunner
	// WatchSummary borrows the already leased Session only for this call.
	WatchSummary func(context.Context, *watchsession.Session) (any, error)
}

// Server holds the lazily connected device service.
type Server struct {
	features   Features
	svc        *garmin.Lazy
	cloud      *gc.Client
	runtime    *garmin.Runtime
	watchDone  <-chan struct{}
	auto       autoState
	version    string
	mcp        *mcpserver.HTTPServer
	probeQuiet time.Duration
	watchEvery time.Duration
	notify     func(context.Context, string, string)
}

// statusResponse is the connection state the UI polls.
type statusResponse struct {
	Connected bool           `json:"connected"`
	Summary   any            `json:"summary,omitempty"`
	Auto      map[string]any `json:"auto,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// ServeRuntime serves an already-bound listener with owners supplied by the
// composition root. It owns the listener and closes the runtime on every exit.
func ServeRuntime(listener net.Listener, runtime *garmin.Runtime, account *gc.Client, autoSync bool, version string, features Features) error {
	ctx, cancel := context.WithCancel(runtime.Context())
	defer cancel()

	s := serverWithRuntime(runtime, account, autoSync, features)

	s.version = version
	addr := listener.Addr().String()

	h, err := s.handler(addr)
	if err != nil {
		closeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer stop()

		return errors.Join(err, listener.Close(), s.runtime.Close(closeCtx))
	}

	if autoSync {
		done := make(chan struct{})

		s.watchDone = done
		go func() { defer close(done); s.watch(ctx) }()
	}

	// No read or write timeout on purpose: track uploads and syncs run for minutes.
	srv := &http.Server{
		Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	var shutdownErr error

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		<-ctx.Done()

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()

		s.runtime.Stop()

		if s.watchDone != nil {
			select {
			case <-s.watchDone:
			case <-sctx.Done():
				shutdownErr = errors.Join(shutdownErr, sctx.Err())
			}
		}

		shutdownErr = errors.Join(shutdownErr, s.runtime.Close(sctx))

		if err := srv.Shutdown(sctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, err, srv.Close())
		}

		shutdownErr = errors.Join(shutdownErr, s.mcp.Close(sctx))
	}()

	slog.Info("garminctl web UI listening", "url", "http://"+addr)

	err = srv.Serve(listener)

	cancel()
	<-stopped

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.Join(err, shutdownErr)
	}

	return shutdownErr
}

func serverWithRuntime(runtime *garmin.Runtime, account *gc.Client, autoSync bool, features Features) *Server {
	s := &Server{features: features, svc: runtime.Watch, runtime: runtime, cloud: account, probeQuiet: watchTick, watchEvery: watchTick, notify: notify.Send}
	s.auto.Enabled = autoSync

	return s
}

// handler builds the routes. addr is the listen address browsers are
// expected to use; requests from other origins are refused.
func (s *Server) handler(addr string) (http.Handler, error) {
	mux := http.NewServeMux()
	s.routes(mux)

	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return nil, err
	}

	ui := http.FileServer(http.FS(sub))
	mux.Handle("/", ui)

	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}

	tabs := map[string]bool{}
	for _, m := range uiTab.FindAllSubmatch(index, -1) {
		tabs[string(m[1])] = true
	}

	// The UI keeps the open tab and item in the path (/apps/EASY+), so a
	// reload or a pasted link gets index.html; any other path is a file.
	page := func(w http.ResponseWriter, r *http.Request) {
		if !tabs[r.PathValue("tab")] {
			ui.ServeHTTP(w, r)
			return
		}

		http.ServeFileFS(w, r, sub, "index.html")
	}
	mux.HandleFunc("GET /{tab}", page)
	mux.HandleFunc("GET /{tab}/{rest...}", page)

	return logRequests(localOnly(addr, mux)), nil
}

// uiTab finds the tab names index.html declares, so the page routes follow
// the nav without a second list here.
var uiTab = regexp.MustCompile(`data-tab="([a-z]+)"`)

func (s *Server) routes(mux *http.ServeMux) {
	for _, registrar := range s.features.HTTP {
		registrar.RegisterRoutes(mux)
	}

	s.mcp = mcpserver.NewHTTPServer(s.runtime, s.cloud, s.version, s.features.MCP...)
	mux.Handle("GET /mcp", s.mcp)
	mux.Handle("POST /mcp", s.mcp)
	mux.Handle("DELETE /mcp", s.mcp)
	mux.HandleFunc("GET /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"endpoint": "http://" + r.Host + "/mcp", "status": s.mcp.Snapshot()})
	})
	mux.HandleFunc("GET /api/server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"application": "garminctl", "version": s.version, "desktopAPI": "1"})
	})
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/connect", s.admittedOperation(s.connect))
	mux.HandleFunc("POST /api/disconnect", s.admittedOperation(s.disconnectHandler))
	mux.HandleFunc("POST /api/refresh", s.admittedOperation(s.refresh))
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		h.ServeHTTP(w, r)

		if strings.HasPrefix(r.URL.Path, "/api/") {
			slog.Info("api", "method", r.Method, "uri", r.URL.RequestURI(), "took", time.Since(start).Round(time.Millisecond))
		}
	})
}

// withService runs fn with a connected service; a bridge failure drops the
// connection so the next call reconnects.
func (s *Server) withService(w http.ResponseWriter, r *http.Request, fn func(context.Context, *garmin.Service) (any, error)) {
	httpx.WithWatch(w, r, s.svc, func(ctx context.Context, lease *garmin.Lease) (any, error) {
		return fn(ctx, lease.Service())
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if s.svc.Manager().Current() == nil && r.URL.Query().Get("connect") == "" {
		writeJSON(w, http.StatusOK, statusResponse{Auto: s.autoSnapshot()})
		return
	}

	httpx.WithWatch(w, r, s.svc.Manager(), func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		sum, err := s.features.WatchSummary(ctx, lease.Session())
		if err != nil {
			return nil, err
		}

		return statusResponse{Connected: true, Summary: sum, Auto: s.autoSnapshot()}, nil
	})
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, s.svc.Manager(), func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		sum, err := s.features.WatchSummary(ctx, lease.Session())
		if err != nil {
			return nil, err
		}

		return statusResponse{Connected: true, Summary: sum}, nil
	})
}

func (s *Server) disconnectHandler(w http.ResponseWriter, _ *http.Request) {
	s.svc.Drop()
	writeJSON(w, http.StatusOK, statusResponse{})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(_ context.Context, svc *garmin.Service) (any, error) {
		svc.Refresh()
		return map[string]any{"ok": true}, nil
	})
}

// admittedOperation admits mutating requests before body reads or account-store
// work, without acquiring a watch or serializing the network through USB.
func (s *Server) admittedOperation(next http.HandlerFunc) http.HandlerFunc {
	return httpx.Admit(s.svc.Gate(), next)
}

// ---------- Garmin Connect ----------

// ---------- Connect IQ store ----------

// withOptionalService keeps offline catalog/account behavior without exporting
// an unleased Service or opening a watch solely to decorate a network response.
func (s *Server) withOptionalService(ctx context.Context, body func(context.Context, *garmin.Service) (any, error)) (any, error) {
	return httpx.WithCurrentWatch(ctx, s.svc, func(ctx context.Context, lease *garmin.Lease) (any, error) {
		var svc *garmin.Service
		if lease != nil {
			svc = lease.Service()
		}

		return body(ctx, svc)
	})
}
