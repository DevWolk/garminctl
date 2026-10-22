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
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"garminctl/internal/ciqstore"
	"garminctl/internal/device"
	"garminctl/internal/garmin"
	"garminctl/internal/gc"
)

//go:embed ui
var uiFS embed.FS

// Opener connects to the device on demand.
type Opener func(ctx context.Context) (device.Device, error)

// Server holds the lazily connected device service.
type Server struct {
	open  Opener
	mu    sync.Mutex
	svc   *garmin.Service
	cloud *gc.Client
	job   syncJob
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
	return map[string]any{"running": j.Running, "startedAt": j.StartedAt, "endedAt": j.EndedAt, "log": append([]string(nil), j.Log...), "result": j.Result, "error": j.Error}
}

// Serve runs the HTTP server until ctx ends.
func Serve(ctx context.Context, addr string, open Opener) error {
	s := &Server{open: open, cloud: gc.New(gc.DefaultStorePath())}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health/activities", s.healthActivities)
	mux.HandleFunc("GET /api/health/activities/{name}", s.healthActivity)
	mux.HandleFunc("GET /api/health/daily", s.healthDaily)
	mux.HandleFunc("GET /api/health/day", s.healthDay)
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
	mux.HandleFunc("POST /api/disconnect", s.disconnect)
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
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	srv := &http.Server{Addr: addr, Handler: logRequests(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		s.disconnectLocked()
	}()
	log.Printf("garminctl web UI on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
		}
	})
}

func (s *Server) service(ctx context.Context) (*garmin.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc != nil {
		return s.svc, nil
	}
	octx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dev, err := s.open(octx)
	if err != nil {
		return nil, err
	}
	s.svc = garmin.New(dev)
	return s.svc, nil
}

func (s *Server) disconnectLocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc != nil {
		_ = s.svc.Close()
		s.svc = nil
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, device.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// withService runs fn with a connected service; a bridge failure drops the
// connection so the next call reconnects.
func (s *Server) withService(w http.ResponseWriter, r *http.Request, fn func(svc *garmin.Service) (any, error)) {
	svc, err := s.service(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "connected": false})
		return
	}
	v, err := fn(svc)
	if err != nil {
		if strings.Contains(err.Error(), "bridge") {
			s.disconnectLocked()
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	connected := s.svc != nil
	s.mu.Unlock()
	if !connected && r.URL.Query().Get("connect") == "" {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		sum, err := svc.Summary(r.Context())
		if err != nil {
			return nil, err
		}
		return map[string]any{"connected": true, "summary": sum}, nil
	})
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		sum, err := svc.Summary(r.Context())
		if err != nil {
			return nil, err
		}
		return map[string]any{"connected": true, "summary": sum}, nil
	})
}

func (s *Server) disconnect(w http.ResponseWriter, _ *http.Request) {
	s.disconnectLocked()
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
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
	svc, err := s.service(r.Context())
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
	if i := strings.LastIndex(p, "/"); i >= 0 {
		name = p[i+1:]
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_, _ = w.Write(data)
}

func (s *Server) filePut(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeErr(w, err)
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PartNumber == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "partNumber required"})
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

func (s *Server) appRemove(w http.ResponseWriter, r *http.Request) {
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		removed, err := svc.RemoveApp(r.Context(), r.PathValue("ref"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"removed": removed, "note": "The watch drops the app from its list when the USB session ends; unplug it."}, nil
	})
}

func (s *Server) appsInstall(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		p, err := svc.InstallPRG(r.Context(), data)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": p, "bytes": len(data), "note": "The watch registers the app when the USB session ends; unplug it."}, nil
	})
}

func (s *Server) appSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]any `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.SetAppSettings(r.Context(), r.PathValue("ref"), body.Values)
	})
}

// ---------- Garmin Connect ----------

func (s *Server) cloudStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	svc := s.svc
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, garmin.NewCloud(svc, s.cloud).Status(r.Context()))
}

func (s *Server) cloudLogin(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email, Password string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "email and password required"})
		return
	}
	res, err := s.cloud.Login(r.Context(), strings.TrimSpace(body.Email), body.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cloudMFA(w http.ResponseWriter, r *http.Request) {
	var body struct{ Code string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "code required"})
		return
	}
	if err := s.cloud.ResumeMFA(r.Context(), body.Code); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) cloudLogout(w http.ResponseWriter, r *http.Request) {
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
	_ = json.NewDecoder(r.Body).Decode(&body)
	up := body.Upload == nil || *body.Upload
	down := body.Download == nil || *body.Download
	svc, err := s.service(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	if !s.cloud.LoggedIn() {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": gc.ErrNotLoggedIn.Error()})
		return
	}
	j := &s.job
	j.mu.Lock()
	if j.Running {
		j.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a sync is already running"})
		return
	}
	j.Running, j.StartedAt, j.EndedAt, j.Log, j.Result, j.Error = true, time.Now(), time.Time{}, nil, nil, ""
	j.mu.Unlock()
	cloud := garmin.NewCloud(svc, s.cloud)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		res, err := cloud.Sync(ctx, up, down, func(line string) {
			j.mu.Lock()
			j.Log = append(j.Log, line)
			j.mu.Unlock()
		})
		j.mu.Lock()
		j.Running, j.EndedAt, j.Result = false, time.Now(), res
		if err != nil {
			j.Error = err.Error()
		}
		j.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, j.snapshot())
}

func (s *Server) cloudSyncState(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Changes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "changes required"})
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
	s.mu.Lock()
	svc := s.svc
	s.mu.Unlock()
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
	view, err := svc.BrowseStore(r.Context(), part, ciqstore.StoreQuery{Keywords: q.Get("q"), Type: q.Get("type"), Sort: q.Get("sort"), Page: page, PageSize: 24})
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
		return svc.Activity(r.Context(), r.PathValue("name"), 1500)
	})
}

func (s *Server) healthDaily(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return garmin.NewCloud(svc, s.cloud).Daily(r.Context(), days)
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
		return garmin.NewCloud(svc, s.cloud).Workouts(r.Context(), 50)
	})
}

type workoutReq struct {
	Spec  garmin.WorkoutSpec `json:"spec"`
	Send  bool               `json:"send"`  // queue it for the watch through Connect
	Local bool               `json:"local"` // write the FIT straight to the watch instead of Connect
}

func (s *Server) workoutsPreview(w http.ResponseWriter, r *http.Request) {
	var req workoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	doc, err := req.Spec.ConnectJSON()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spec": req.Spec, "connect": doc})
}

func (s *Server) workoutsCreate(w http.ResponseWriter, r *http.Request) {
	var req workoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		if req.Local {
			p, err := svc.WriteWorkoutToWatch(r.Context(), &req.Spec)
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": p, "note": "Workout file written to GARMIN/NewFiles; unplug the watch and find it under Training > Workouts."}, nil
		}
		cloud := garmin.NewCloud(svc, s.cloud)
		wk, err := cloud.CreateWorkout(r.Context(), &req.Spec)
		if err != nil {
			return nil, err
		}
		res := map[string]any{"workout": wk, "note": "Saved in Garmin Connect."}
		if req.Send {
			pulled, err := cloud.SendWorkout(r.Context(), wk.ID, wk.Name)
			if err != nil {
				res["note"] = "Saved in Garmin Connect, but sending to the watch failed: " + err.Error()
			} else {
				res["pulled"] = pulled
				if len(pulled) > 0 {
					res["note"] = "Saved in Garmin Connect and written to the watch; unplug it to see the workout under Training > Workouts."
				} else {
					res["note"] = "Saved in Garmin Connect and queued for the watch; Sync later or let the phone deliver it."
				}
			}
		}
		return res, nil
	})
}

func (s *Server) workoutsSend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad workout id"})
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad workout id"})
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
	data, err := io.ReadAll(io.LimitReader(r.Body, 512<<20))
	if err != nil {
		writeErr(w, err)
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.withService(w, r, func(svc *garmin.Service) (any, error) {
		return svc.WritePlaylist(r.Context(), r.PathValue("name"), body.Tracks)
	})
}
