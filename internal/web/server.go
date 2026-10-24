// Package web serves the browser UI and the JSON API.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/mcpserver"
)

//go:embed ui
var uiFS embed.FS

const (
	shutdownTimeout = 3 * time.Second
	maxUploadBytes  = 64 << 20
	maxTrackBytes   = 512 << 20
	maxJSONBody     = 1 << 20
	noteKey         = "note" // human-readable remark on a result object
	rangeDays       = 14     // beyond this the range endpoints replace one request per day
)

// Server holds the lazily connected device service.
type Server struct {
	svc     *garmin.Lazy
	cloud   *gc.Client
	job     syncJob
	auto    autoState
	version string
	mcp     *mcpserver.HTTPServer
}

// syncJob is the one background sync run at a time.
type syncJob struct {
	mu        sync.Mutex
	Running   bool               `json:"running"`
	StartedAt time.Time          `json:"startedAt"`
	EndedAt   time.Time          `json:"endedAt"`
	Log       []string           `json:"log"`
	Result    *garmin.SyncResult `json:"result,omitempty"`
	Error     string             `json:"error,omitempty"`
}

func (j *syncJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()

	return map[string]any{
		"running": j.Running, "startedAt": j.StartedAt, "endedAt": j.EndedAt,
		"log": append([]string(nil), j.Log...), "result": j.Result, "error": j.Error,
	}
}

// statusResponse is the connection state the UI polls.
type statusResponse struct {
	Connected bool           `json:"connected"`
	Summary   any            `json:"summary,omitempty"`
	Auto      map[string]any `json:"auto,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// Serve runs the HTTP server until ctx ends.
func Serve(ctx context.Context, addr string, open garmin.Opener, autoSync bool, version string) error {
	var config net.ListenConfig

	listener, err := config.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return ServeListener(ctx, listener, open, autoSync, version)
}

// ServeListener serves an already-bound listener and owns its lifetime. Desktop
// launchers use it to reserve a port before showing the window.
func ServeListener(ctx context.Context, listener net.Listener, open garmin.Opener, autoSync bool, version string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := newServer(open, autoSync)

	s.version = version
	defer s.svc.Drop()

	if autoSync {
		go s.watch(ctx)
	}

	addr := listener.Addr().String()

	h, err := s.handler(addr)
	if err != nil {
		_ = listener.Close()
		return err
	}

	// No read or write timeout on purpose: track uploads and syncs run for minutes.
	srv := &http.Server{
		Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		<-ctx.Done()

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(sctx); err != nil {
			_ = srv.Close()
		}
	}()

	slog.Info("garminctl web UI listening", "url", "http://"+addr)

	err = srv.Serve(listener)

	cancel()
	<-stopped

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func newServer(open garmin.Opener, autoSync bool) *Server {
	s := &Server{svc: garmin.NewLazy(open), cloud: gc.New(gc.DefaultStorePath())}
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
	s.mcp = mcpserver.NewHTTPServer(s.svc, s.cloud, s.version)
	mux.Handle("GET /mcp", s.mcp)
	mux.Handle("POST /mcp", s.mcp)
	mux.Handle("DELETE /mcp", s.mcp)
	mux.HandleFunc("GET /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"endpoint": "http://" + r.Host + "/mcp", "status": s.mcp.Snapshot()})
	})
	mux.HandleFunc("GET /api/server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"application": "garminctl", "version": s.version, "desktopAPI": "1"})
	})
	mux.HandleFunc("GET /api/health/activities", s.healthActivities)
	mux.HandleFunc("GET /api/health/activities/{name}", s.healthActivity)
	mux.HandleFunc("GET /api/health/daily", s.healthDaily)
	mux.HandleFunc("GET /api/health/day", s.healthDay)
	mux.HandleFunc("GET /api/health/extras", s.healthExtras)
	mux.HandleFunc("POST /api/health/weight", s.healthWeight)
	mux.HandleFunc("GET /api/workouts", s.workoutsList)
	mux.HandleFunc("POST /api/workouts", s.workoutsCreate)
	mux.HandleFunc("POST /api/workouts/{id}/send", s.workoutsSend)
	mux.HandleFunc("DELETE /api/workouts/{id}", s.workoutsDelete)
	mux.HandleFunc("POST /api/workouts/preview", s.workoutsPreview)
	mux.HandleFunc("GET /api/music", s.musicList)
	mux.HandleFunc("POST /api/music", s.musicAdd)
	mux.HandleFunc("DELETE /api/music", s.musicRemove)
	mux.HandleFunc("PUT /api/music/playlists/{name}", s.musicPlaylist)
	mux.HandleFunc("GET /api/store", s.storeBrowse)
	mux.HandleFunc("GET /api/store/apps/{id}", s.storeApp)
	mux.HandleFunc("GET /api/cloud/status", s.cloudStatus)
	mux.HandleFunc("POST /api/cloud/login", s.cloudLogin)
	mux.HandleFunc("POST /api/cloud/mfa", s.cloudMFA)
	mux.HandleFunc("POST /api/cloud/logout", s.cloudLogout)
	mux.HandleFunc("POST /api/cloud/sync", s.cloudSyncStart)
	mux.HandleFunc("GET /api/cloud/sync", s.cloudSyncState)
	mux.HandleFunc("GET /api/cloud/messages", s.cloudMessages)
	mux.HandleFunc("GET /api/cloud/settings", s.cloudSettings)
	mux.HandleFunc("PUT /api/cloud/settings", s.cloudSettingsPut)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/connect", s.connect)
	mux.HandleFunc("POST /api/disconnect", s.disconnectHandler)
	mux.HandleFunc("POST /api/refresh", s.refresh)
	mux.HandleFunc("GET /api/files", s.files)
	mux.HandleFunc("GET /api/file", s.fileGet)
	mux.HandleFunc("POST /api/file", s.filePut)
	mux.HandleFunc("DELETE /api/file", s.fileDelete)
	mux.HandleFunc("GET /api/decode", s.decode)
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("POST /api/settings/apply", s.settingsApply)
	mux.HandleFunc("GET /api/schema", s.schema)
	mux.HandleFunc("GET /api/apps", s.apps)
	mux.HandleFunc("GET /api/apps/{ref}/settings", s.appSettings)
	mux.HandleFunc("PUT /api/apps/{ref}/settings", s.appSettingsPut)
	mux.HandleFunc("GET /api/apps/{ref}/schema", s.appSchema)
	mux.HandleFunc("GET /api/updates/software", s.updatesSoftware)
	mux.HandleFunc("POST /api/updates/software/install", s.updatesInstall)
	mux.HandleFunc("GET /api/updates/apps", s.updatesApps)
	mux.HandleFunc("GET /api/software", s.software)
	mux.HandleFunc("POST /api/apps/install", s.appsInstall)
	mux.HandleFunc("DELETE /api/apps/{ref}", s.appRemove)
	mux.HandleFunc("PUT /api/apps/{ref}/hidden", s.appHidden)
	mux.HandleFunc("GET /api/apps/pending", s.appPending)
	mux.HandleFunc("DELETE /api/apps/pending", s.appPendingCancel)
}

// localOnly refuses requests that did not come from a page this server
// served. The server has no authentication: anything that can reach it can
// write to the watch, so a web page from elsewhere must not be able to call
// the API through the browser (cross-site requests, DNS rebinding).
func localOnly(addr string, next http.Handler) http.Handler {
	listenHost, port, err := net.SplitHostPort(addr)
	if err != nil {
		listenHost, port = "", ""
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, listenHost, port) {
			http.Error(w, "forbidden: unexpected Host", http.StatusForbidden)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
			return
		}

		if crossSiteCall(r) {
			http.Error(w, "forbidden: cross-site request", http.StatusForbidden)
			return
		}

		// Another site must not frame the UI and click its buttons.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")

		next.ServeHTTP(w, r)
	})
}

// crossSiteCall reports a browser request made by another site's page. A
// plain navigation into the UI (the user following a link) is fine: it
// carries no body and only renders the page. A navigation straight to an
// API path is not: some GET endpoints act (connect to the watch, wait for
// a file, send the device XML to Garmin), and a link is enough to fire
// them. Everything else from another site, fetches as well as form posts,
// is refused.
func crossSiteCall(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
	default:
		return false
	}

	isNavigation := r.Header.Get("Sec-Fetch-Mode") == "navigate"
	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead
	isPage := !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/mcp"

	return !isNavigation || !isRead || !isPage
}

// hostAllowed accepts a Host header that names this server on its port:
// localhost, any IP literal (the address the user typed, on the LAN too) or
// the name the server was told to listen on. DNS rebinding always arrives
// under some other name, which is what this refuses.
func hostAllowed(host, listenHost, port string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p = host, ""
	}

	if port != "" && p != port {
		return false
	}

	h = strings.Trim(h, "[]")
	if h == "localhost" || (listenHost != "" && strings.EqualFold(h, strings.Trim(listenHost, "[]"))) {
		return true
	}

	return net.ParseIP(h) != nil
}

func sameOrigin(origin, host string) bool {
	return origin == "http://"+host
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	_ = enc.Encode(v)
}

// readBody reads an upload of at most limit bytes. A larger one is refused
// with 413; cutting it short would write a truncated file to the watch.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body larger than %d bytes", limit))
			return nil, false
		}

		writeError(w, http.StatusBadRequest, err.Error())

		return nil, false
	}

	return data, true
}

// writeError answers with {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, device.ErrNotFound) {
		status = http.StatusNotFound
	}

	writeError(w, status, err.Error())
}

// withService runs fn with a connected service; a bridge failure drops the
// connection so the next call reconnects.
func (s *Server) withService(w http.ResponseWriter, r *http.Request, fn func(svc *garmin.Service) (any, error)) {
	svc, err := s.svc.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, statusResponse{Error: err.Error()})
		return
	}

	v, err := fn(svc)
	if err != nil {
		if strings.Contains(err.Error(), "bridge") {
			s.svc.Drop()
		}

		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, v)
}

// decodeBody reads a JSON request body into v and answers 400 on failure.
// The body is capped at maxJSONBody and must hold exactly one JSON value.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}

	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "trailing data after the JSON body")
		return false
	}

	return true
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if s.svc.Current() == nil && r.URL.Query().Get("connect") == "" {
		writeJSON(w, http.StatusOK, statusResponse{Auto: s.autoSnapshot()})
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		sum, err := svc.Summary(r.Context())
		if err != nil {
			return nil, err
		}

		return statusResponse{Connected: true, Summary: sum, Auto: s.autoSnapshot()}, nil
	})
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		sum, err := svc.Summary(r.Context())
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
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		svc.Refresh()
		return map[string]any{"ok": true}, nil
	})
}

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.Device().List(r.Context(), r.URL.Query().Get("path"))
	})
}

func (s *Server) fileGet(w http.ResponseWriter, r *http.Request) {
	svc, err := s.svc.Get(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	p := r.URL.Query().Get("path")

	data, err := svc.Device().Read(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}

	name := p
	if _, after, ok := strings.CutLast(p, "/"); ok {
		name = after
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_, _ = w.Write(data) //nolint:gosec // a download of a watch file: fixed binary content type, nosniff, attachment
}

func (s *Server) filePut(w http.ResponseWriter, r *http.Request) {
	data, ok := readBody(w, r, maxUploadBytes)
	if !ok {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		p := r.URL.Query().Get("path")
		if err := svc.Device().Write(r.Context(), p, data); err != nil {
			return nil, err
		}

		svc.Refresh()

		return map[string]any{"ok": true, "path": p, "bytes": len(data)}, nil
	})
}

func (s *Server) fileDelete(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		p := r.URL.Query().Get("path")
		if err := svc.Device().Delete(r.Context(), p); err != nil {
			return nil, err
		}

		svc.Refresh()

		return map[string]any{"ok": true}, nil
	})
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.DecodeFIT(r.Context(), r.URL.Query().Get("path"))
	})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		view, err := svc.Settings(r.Context())
		if err != nil {
			return nil, err
		}

		return map[string]any{"rows": view.Rows()}, nil
	})
}

func (s *Server) settingsApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Changes []garmin.Change `json:"changes"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.ApplyChanges(r.Context(), body.Changes)
	})
}

func (s *Server) schema(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.Schema(r.URL.Query().Get("mesg"))
	})
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.Apps(r.Context())
	})
}

func (s *Server) appSettings(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.AppSettings(r.Context(), r.PathValue("ref"))
	})
}

func (s *Server) appSchema(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.AppSchema(r.Context(), r.PathValue("ref"))
	})
}

func (s *Server) updatesSoftware(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.CheckSoftwareUpdates(r.Context())
	})
}

func (s *Server) updatesInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PartNumber string `json:"partNumber"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	if body.PartNumber == "" {
		writeError(w, http.StatusBadRequest, "partNumber required")
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.InstallSoftwareUpdate(r.Context(), body.PartNumber)
	})
}

func (s *Server) updatesApps(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.CheckAppUpdates(r.Context())
	})
}

func (s *Server) software(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.SoftwareInventory(r.Context())
	})
}

func (s *Server) appHidden(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hidden bool `json:"hidden"`
	}

	if !decodeBody(w, r, &in) {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		backup, err := svc.SetAppHidden(r.Context(), r.PathValue("ref"), in.Hidden)
		if err != nil {
			return nil, err
		}

		return map[string]any{"hidden": in.Hidden, "backup": backup, noteKey: "Applied when the USB session ends; unplug the watch."}, nil
	})
}

func (s *Server) appPending(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) { return svc.PendingAppChanges(r.Context()) })
}

func (s *Server) appPendingCancel(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		if err := svc.CancelAppChanges(r.Context()); err != nil {
			return nil, err
		}

		return map[string]any{"ok": true}, nil
	})
}

func (s *Server) appRemove(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		removed, err := svc.RemoveApp(r.Context(), r.PathValue("ref"))
		if err != nil {
			return nil, err
		}

		return map[string]any{"removed": removed, noteKey: "The watch drops the app from its list when the USB session ends; unplug it."}, nil
	})
}

func (s *Server) appsInstall(w http.ResponseWriter, r *http.Request) {
	data, ok := readBody(w, r, maxUploadBytes)
	if !ok {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		p, err := svc.InstallPRG(r.Context(), data)
		if err != nil {
			return nil, err
		}

		return map[string]any{"path": p, "bytes": len(data), noteKey: "The watch registers the app when the USB session ends; unplug it."}, nil
	})
}

func (s *Server) appSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]any `json:"values"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.SetAppSettings(r.Context(), r.PathValue("ref"), body.Values)
	})
}

// ---------- Garmin Connect ----------

func (s *Server) cloudStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, garmin.NewCloud(s.svc.Current(), s.cloud).Status(r.Context()))
}

func (s *Server) cloudLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	if body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}

	res, err := s.cloud.Login(r.Context(), strings.TrimSpace(body.Email), body.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cloudMFA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	if body.Code == "" {
		writeError(w, http.StatusBadRequest, "code required")
		return
	}

	if err := s.cloud.ResumeMFA(r.Context(), body.Code); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) cloudLogout(w http.ResponseWriter, _ *http.Request) {
	if err := s.cloud.Logout(); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) cloudSyncStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Upload   *bool `json:"upload"`
		Download *bool `json:"download"`
	}

	// An empty body means both directions; anything else must be valid JSON.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	up := body.Upload == nil || *body.Upload
	down := body.Download == nil || *body.Download

	svc, err := s.svc.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	if !s.cloud.LoggedIn() {
		writeError(w, http.StatusUnauthorized, gc.ErrNotLoggedIn.Error())
		return
	}

	if _, started := s.startSync(r.Context(), svc, up, down); !started {
		writeError(w, http.StatusConflict, "a sync is already running")
		return
	}

	writeJSON(w, http.StatusAccepted, s.job.snapshot())
}

func (s *Server) cloudSyncState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.job.snapshot())
}

func (s *Server) cloudMessages(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Messages(r.Context())
	})
}

func (s *Server) cloudSettings(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Settings(r.Context())
	})
}

func (s *Server) cloudSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Changes map[string]any `json:"changes"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	if len(body.Changes) == 0 {
		writeError(w, http.StatusBadRequest, "changes required")
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).SetSettings(r.Context(), body.Changes)
	})
}

// ---------- Connect IQ store ----------

const defaultPart = "006-B4426-00" // vívoactive 5 firmware part; replaced by the connected watch's

// storeSvc returns the connected service (or nil) and the watch's part number.
func (s *Server) storeSvc(ctx context.Context) (*garmin.Service, string) {
	svc := s.svc.Current()
	part := defaultPart

	if svc != nil {
		if dx, err := svc.DeviceXML(ctx); err == nil && dx.PartNumber != "" {
			part = dx.PartNumber
		}
	}

	return svc, part
}

func (s *Server) storeBrowse(w http.ResponseWriter, r *http.Request) {
	svc, part := s.storeSvc(r.Context())
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))

	query := ciqstore.StoreQuery{Keywords: q.Get("q"), Type: q.Get("type"), Sort: q.Get("sort"), Page: page, PageSize: 0} // the store's default page

	view, err := svc.BrowseStore(r.Context(), part, query)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, view)
}

func (s *Server) storeApp(w http.ResponseWriter, r *http.Request) {
	svc, part := s.storeSvc(r.Context())

	d, err := svc.StoreApp(r.Context(), part, r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, d)
}

// ---------- health ----------

func (s *Server) healthActivities(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.Activities(r.Context(), r.URL.Query().Get("wait") == "1")
	})
}

func (s *Server) healthActivity(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.Activity(r.Context(), r.PathValue("name"), 0) // default track resolution
	})
}

func (s *Server) healthDaily(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		if days > rangeDays {
			return garmin.NewCloud(svc, s.cloud).History(r.Context(), days)
		}

		return garmin.NewCloud(svc, s.cloud).Daily(r.Context(), days)
	})
}

func (s *Server) healthExtras(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Extras(r.Context())
	})
}

func (s *Server) healthWeight(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kg float64 `json:"kg"`
	}

	if !decodeBody(w, r, &in) {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		if err := garmin.NewCloud(svc, s.cloud).LogWeight(r.Context(), in.Kg, time.Time{}); err != nil {
			return nil, err
		}

		return map[string]any{"ok": true, "kg": in.Kg}, nil
	})
}

func (s *Server) healthDay(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Day(r.Context(), r.URL.Query().Get("date"))
	})
}

// ---------- workouts ----------

func (s *Server) workoutsList(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Workouts(r.Context(), 0) // default page
	})
}

type workoutReq struct {
	Spec  garmin.WorkoutSpec `json:"spec"`
	Send  bool               `json:"send"`  // queue it for the watch through Connect
	Local bool               `json:"local"` // write the FIT straight to the watch instead of Connect
}

func (s *Server) workoutsPreview(w http.ResponseWriter, r *http.Request) {
	var req workoutReq
	if !decodeBody(w, r, &req) {
		return
	}

	doc, err := req.Spec.ConnectJSON()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"spec": req.Spec, "connect": doc})
}

func (s *Server) workoutsCreate(w http.ResponseWriter, r *http.Request) {
	var req workoutReq
	if !decodeBody(w, r, &req) {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		if req.Local {
			p, err := svc.WriteWorkoutToWatch(r.Context(), &req.Spec)
			if err != nil {
				return nil, err
			}

			return map[string]any{"path": p, noteKey: "Workout file written to GARMIN/NewFiles; unplug the watch and find it under Training > Workouts."}, nil
		}

		cloud := garmin.NewCloud(svc, s.cloud)

		wk, err := cloud.CreateWorkout(r.Context(), &req.Spec)
		if err != nil {
			return nil, err
		}

		res := map[string]any{"workout": wk}
		if !req.Send {
			res[noteKey] = "Saved in Garmin Connect."

			return res, nil
		}

		pulled, err := cloud.SendWorkout(r.Context(), wk.ID, wk.Name)
		if err != nil {
			res[noteKey] = "Saved in Garmin Connect, but sending to the watch failed: " + err.Error()

			return res, nil //nolint:nilerr // partial success: the workout is saved in Connect; the send failure is reported in the note
		}

		res["pulled"] = pulled
		res[noteKey] = sendNote(len(pulled))

		return res, nil
	})
}

// sendNote describes what happened after Connect was asked to queue a workout.
func sendNote(pulled int) string {
	if pulled > 0 {
		return "Saved in Garmin Connect and written to the watch; unplug it to see the workout under Training > Workouts."
	}

	return "Saved in Garmin Connect and queued for the watch; Sync later or let the phone deliver it."
}

func (s *Server) workoutsSend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad workout id")
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		pulled, err := garmin.NewCloud(svc, s.cloud).SendWorkout(r.Context(), id, r.URL.Query().Get("name"))
		if err != nil {
			return nil, err
		}

		return map[string]any{"pulled": pulled}, nil
	})
}

func (s *Server) workoutsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad workout id")
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return map[string]any{"ok": true}, garmin.NewCloud(svc, s.cloud).DeleteWorkout(r.Context(), id)
	})
}

// ---------- music ----------

func (s *Server) musicList(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) { return svc.Music(r.Context()) })
}

func (s *Server) musicAdd(w http.ResponseWriter, r *http.Request) {
	data, ok := readBody(w, r, maxTrackBytes)
	if !ok {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.AddTrack(r.Context(), r.URL.Query().Get("folder"), r.URL.Query().Get("name"), data)
	})
}

func (s *Server) musicRemove(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return map[string]any{"ok": true}, svc.RemoveTrack(r.Context(), r.URL.Query().Get("path"))
	})
}

func (s *Server) musicPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tracks []string `json:"tracks"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.WritePlaylist(r.Context(), r.PathValue("name"), body.Tracks)
	})
}
