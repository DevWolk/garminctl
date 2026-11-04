// Package web serves the browser UI and the JSON API.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
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
	rangeDays       = 14     // beyond this the range endpoints replace one request per day
)

// Features contains adapters assembled by the composition root. HTTP and MCP
// shells receive registrations, never import the feature implementations.
type Features struct {
	HTTP []httpx.RouteRegistrar
	MCP  []mcpx.ToolRegistrar
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

func (s *Server) syncSnapshot() map[string]any {
	state := s.runtime.Sync.Snapshot()

	return map[string]any{
		"running": state.Running, "startedAt": state.StartedAt, "endedAt": state.EndedAt,
		"log": state.Log, "result": state.Result, "error": state.Error,
	}
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
	mux.HandleFunc("GET /api/health/activities", s.healthActivities)
	mux.HandleFunc("GET /api/health/activities/{name}", s.healthActivity)
	mux.HandleFunc("GET /api/health/daily", s.healthDaily)
	mux.HandleFunc("GET /api/health/day", s.healthDay)
	mux.HandleFunc("GET /api/health/extras", s.healthExtras)
	mux.HandleFunc("POST /api/health/weight", s.admittedOperation(s.healthWeight))
	mux.HandleFunc("GET /api/workouts", s.workoutsList)
	mux.HandleFunc("POST /api/workouts", s.admittedOperation(s.workoutsCreate))
	mux.HandleFunc("POST /api/workouts/{id}/send", s.admittedOperation(s.workoutsSend))
	mux.HandleFunc("DELETE /api/workouts/{id}", s.admittedOperation(s.workoutsDelete))
	mux.HandleFunc("POST /api/workouts/preview", s.admittedOperation(s.workoutsPreview))
	mux.HandleFunc("GET /api/music", s.musicList)
	mux.HandleFunc("POST /api/music", s.admittedOperation(s.musicAdd))
	mux.HandleFunc("DELETE /api/music", s.admittedOperation(s.musicRemove))
	mux.HandleFunc("PUT /api/music/playlists/{name}", s.admittedOperation(s.musicPlaylist))
	mux.HandleFunc("GET /api/cloud/status", s.cloudStatus)
	mux.HandleFunc("POST /api/cloud/login", s.admittedOperation(s.cloudLogin))
	mux.HandleFunc("POST /api/cloud/mfa", s.admittedOperation(s.cloudMFA))
	mux.HandleFunc("POST /api/cloud/logout", s.admittedOperation(s.cloudLogout))
	mux.HandleFunc("POST /api/cloud/sync", s.admittedOperation(s.cloudSyncStart))
	mux.HandleFunc("GET /api/cloud/sync", s.cloudSyncState)
	mux.HandleFunc("GET /api/cloud/messages", s.cloudMessages)
	mux.HandleFunc("GET /api/cloud/settings", s.cloudSettings)
	mux.HandleFunc("PUT /api/cloud/settings", s.admittedOperation(s.cloudSettingsPut))
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

func (s *Server) cloudStatus(w http.ResponseWriter, r *http.Request) {
	out, err := s.withOptionalService(r.Context(), func(ctx context.Context, svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Status(ctx), nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, out)
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

	if s.cloud.LoggedIn() && s.runtime.Sync.Snapshot().Running {
		writeError(w, http.StatusConflict, "a sync is already running")
		return
	}

	lease, err := s.svc.Acquire(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	if !s.cloud.LoggedIn() {
		_ = lease.Release()

		writeError(w, http.StatusUnauthorized, gc.ErrNotLoggedIn.Error())

		return
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		writeErr(w, err)
		return
	}

	if _, _, err := s.startSync(r.Context(), epoch, up, down); err != nil {
		if errors.Is(err, garmin.ErrSyncBusy) {
			writeError(w, http.StatusConflict, "a sync is already running")
		} else {
			writeErr(w, err)
		}

		return
	}

	writeJSON(w, http.StatusAccepted, s.syncSnapshot())
}

func (s *Server) cloudSyncState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.syncSnapshot())
}

func (s *Server) cloudMessages(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Messages(ctx)
	})
}

func (s *Server) cloudSettings(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Settings(ctx)
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

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).SetSettings(ctx, body.Changes)
	})
}

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

// ---------- health ----------

func (s *Server) healthActivities(w http.ResponseWriter, r *http.Request) {
	out, err := s.runtime.Activities.Activities(r.Context(), r.URL.Query().Get("wait") == "1")
	if err != nil {
		if _, ok := errors.AsType[*garmin.WatchAcquireError](err); ok {
			writeJSON(w, deviceErrorStatus(err, http.StatusServiceUnavailable), statusResponse{Error: err.Error()})
			return
		}

		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) healthActivity(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return svc.Activity(ctx, r.PathValue("name"), 0) // default track resolution
	})
}

func (s *Server) healthDaily(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		if days > rangeDays {
			return garmin.NewCloud(svc, s.cloud).History(ctx, days)
		}

		return garmin.NewCloud(svc, s.cloud).Daily(ctx, days)
	})
}

func (s *Server) healthExtras(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Extras(ctx)
	})
}

func (s *Server) healthWeight(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kg float64 `json:"kg"`
	}

	if !decodeBody(w, r, &in) {
		return
	}

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		if err := garmin.NewCloud(svc, s.cloud).LogWeight(ctx, in.Kg, time.Time{}); err != nil {
			return nil, err
		}

		return map[string]any{"ok": true, "kg": in.Kg}, nil
	})
}

func (s *Server) healthDay(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Day(ctx, r.URL.Query().Get("date"))
	})
}

// ---------- workouts ----------

func (s *Server) workoutsList(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Workouts(ctx, 0) // default page
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

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		if req.Local {
			p, err := svc.WriteWorkoutToWatch(ctx, &req.Spec)
			if err != nil {
				return nil, err
			}

			return map[string]any{"path": p, noteKey: "Workout file written to GARMIN/NewFiles; unplug the watch and find it under Training > Workouts."}, nil
		}

		cloud := garmin.NewCloud(svc, s.cloud)

		wk, err := cloud.CreateWorkout(ctx, &req.Spec)
		if err != nil {
			return nil, err
		}

		res := map[string]any{"workout": wk}
		if !req.Send {
			res[noteKey] = "Saved in Garmin Connect."

			return res, nil
		}

		pulled, err := cloud.SendWorkout(ctx, wk.ID, wk.Name)
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

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		pulled, err := garmin.NewCloud(svc, s.cloud).SendWorkout(ctx, id, r.URL.Query().Get("name"))
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

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return map[string]any{"ok": true}, garmin.NewCloud(svc, s.cloud).DeleteWorkout(ctx, id)
	})
}

// ---------- music ----------

func (s *Server) musicList(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) { return svc.Music(ctx) })
}

func (s *Server) musicAdd(w http.ResponseWriter, r *http.Request) {
	data, ok := readBody(w, r, maxTrackBytes)
	if !ok {
		return
	}

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return svc.AddTrack(ctx, r.URL.Query().Get("folder"), r.URL.Query().Get("name"), data)
	})
}

func (s *Server) musicRemove(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return map[string]any{"ok": true}, svc.RemoveTrack(ctx, r.URL.Query().Get("path"))
	})
}

func (s *Server) musicPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tracks []string `json:"tracks"`
	}

	if !decodeBody(w, r, &body) {
		return
	}

	s.withService(w, r, func(ctx context.Context, svc *garmin.Service) (any, error) {
		return svc.WritePlaylist(ctx, r.PathValue("name"), body.Tracks)
	})
}
