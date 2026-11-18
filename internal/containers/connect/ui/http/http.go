// Package http adapts Connect Actions to the existing account and sync API.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Handler borrows the process account, coordinator and session factories.
type Handler struct {
	Manager     *watchsession.Manager
	Client      *gc.Client
	Coordinator *connect.SyncCoordinator
	CloudFor    func(*watchsession.Session) *connect.Cloud
	JobFor      func(watchsession.ConnectionEpoch) *connect.Cloud
	// ForgetSessionCopies is supplied by the composition that keeps copies of
	// the saved session; sign-out deletes them.
	ForgetSessionCopies func(context.Context) error
}

// RegisterRoutes preserves nine account/settings/queue/sync routes and admission.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cloud/status", h.cloudStatus)
	mux.HandleFunc("POST /api/cloud/login", httpx.Admit(h.Manager.Gate(), h.cloudLogin))
	mux.HandleFunc("POST /api/cloud/mfa", httpx.Admit(h.Manager.Gate(), h.cloudMFA))
	mux.HandleFunc("POST /api/cloud/logout", httpx.Admit(h.Manager.Gate(), h.cloudLogout))
	mux.HandleFunc("POST /api/cloud/sync", httpx.Admit(h.Manager.Gate(), h.cloudSyncStart))
	mux.HandleFunc("GET /api/cloud/sync", h.cloudSyncState)
	mux.HandleFunc("GET /api/cloud/messages", h.cloudMessages)
	mux.HandleFunc("GET /api/cloud/settings", h.cloudSettings)
	mux.HandleFunc("PUT /api/cloud/settings", httpx.Admit(h.Manager.Gate(), h.cloudSettingsPut))
}

// Snapshot preserves the shared web sync-state response shape.
func Snapshot(coordinator *connect.SyncCoordinator) map[string]any {
	state := coordinator.Snapshot()
	return map[string]any{"running": state.Running, "startedAt": state.StartedAt, "endedAt": state.EndedAt, "log": state.Log, "result": state.Result, "error": state.Error}
}

func (h *Handler) startSync(ctx context.Context, epoch watchsession.ConnectionEpoch, up, down bool) (*connect.SyncJob, bool, error) {
	cloud := h.JobFor(epoch)

	return h.Coordinator.Start(ctx, connect.SyncOptions{Upload: up, Download: down, Epoch: epoch}, func(ctx context.Context, progress connect.Progress) (*connect.SyncResult, error) {
		return (connect.SyncAction{Cloud: cloud}).Run(ctx, up, down, progress)
	})
}

func (h *Handler) cloudStatus(w http.ResponseWriter, r *http.Request) {
	out, err := httpx.WithCurrentWatch(r.Context(), h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		var sess *watchsession.Session
		if lease != nil {
			sess = lease.Session()
		}

		return (connect.StatusAction{Cloud: h.CloudFor(sess)}).Run(ctx), nil
	})
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) cloudLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if !httpx.DecodeBody(w, r, &body) {
		return
	}

	if body.Email == "" || body.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, "email and password required")
		return
	}

	res, err := (connect.LoginAction{Client: h.Client}).Run(r.Context(), strings.TrimSpace(body.Email), body.Password)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) cloudMFA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}

	if !httpx.DecodeBody(w, r, &body) {
		return
	}

	if body.Code == "" {
		httpx.WriteError(w, http.StatusBadRequest, "code required")
		return
	}

	if err := (connect.MFAAction{Client: h.Client}).Run(r.Context(), body.Code); err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) cloudLogout(w http.ResponseWriter, r *http.Request) {
	if err := (connect.LogoutAction{Client: h.Client, ForgetCopies: h.ForgetSessionCopies}).Run(r.Context()); err != nil {
		httpx.WriteErr(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) cloudSyncStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Upload   *bool `json:"upload"`
		Download *bool `json:"download"`
	}

	// An empty body means both directions; anything else must be valid JSON.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, httpx.MaxJSONBody))
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	up := body.Upload == nil || *body.Upload
	down := body.Download == nil || *body.Download

	if h.Client.LoggedIn() && h.Coordinator.Snapshot().Running {
		httpx.WriteError(w, http.StatusConflict, "a sync is already running")
		return
	}

	lease, err := h.Manager.Acquire(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	if !h.Client.LoggedIn() {
		_ = lease.Release()

		httpx.WriteError(w, http.StatusUnauthorized, gc.ErrNotLoggedIn.Error())

		return
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		httpx.WriteErr(w, err)
		return
	}

	if _, _, err := h.startSync(r.Context(), epoch, up, down); err != nil {
		if errors.Is(err, connect.ErrSyncBusy) {
			httpx.WriteError(w, http.StatusConflict, "a sync is already running")
		} else {
			httpx.WriteErr(w, err)
		}

		return
	}

	httpx.WriteJSON(w, http.StatusAccepted, Snapshot(h.Coordinator))
}

func (h *Handler) cloudSyncState(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, Snapshot(h.Coordinator))
}

func (h *Handler) cloudMessages(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (connect.MessagesAction{Cloud: h.CloudFor(lease.Session())}).Run(ctx)
	})
}

func (h *Handler) cloudSettings(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (connect.SettingsAction{Cloud: h.CloudFor(lease.Session())}).Run(ctx)
	})
}

func (h *Handler) cloudSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Changes map[string]any `json:"changes"`
	}

	if !httpx.DecodeBody(w, r, &body) {
		return
	}

	if len(body.Changes) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "changes required")
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (connect.ApplySettingsAction{Cloud: h.CloudFor(lease.Session())}).Run(ctx, body.Changes)
	})
}
