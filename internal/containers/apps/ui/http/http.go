// Package http adapts installed-app Actions to the local HTTP API.
package http

import (
	"context"
	"net/http"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const noteKey = "note"

// Handler uses the backend's existing watch owner and per-epoch schema state.
// The HTTP shell applies origin guards/logging to the assembled mux.
type Handler struct {
	Manager  *watchsession.Manager
	CacheFor func(*watchsession.Session) *apps.SchemaCache
}

// RegisterRoutes preserves the existing apps API paths and mutating admission.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/apps", h.apps)
	mux.HandleFunc("GET /api/apps/{ref}/settings", h.appSettings)
	mux.HandleFunc("PUT /api/apps/{ref}/settings", httpx.Admit(h.Manager.Gate(), h.appSettingsPut))
	mux.HandleFunc("GET /api/apps/{ref}/schema", h.appSchema)
	mux.HandleFunc("POST /api/apps/install", httpx.Admit(h.Manager.Gate(), h.appsInstall))
	mux.HandleFunc("DELETE /api/apps/{ref}", httpx.Admit(h.Manager.Gate(), h.appRemove))
	mux.HandleFunc("PUT /api/apps/{ref}/hidden", httpx.Admit(h.Manager.Gate(), h.appHidden))
	mux.HandleFunc("GET /api/apps/pending", h.appPending)
	mux.HandleFunc("DELETE /api/apps/pending", httpx.Admit(h.Manager.Gate(), h.appPendingCancel))
}

func (h *Handler) apps(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (apps.ReadRegistryAction{}).Run(ctx, lease.Session())
	})
}

func (h *Handler) appSettings(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return apps.NewReadSettingsAction(h.CacheFor(lease.Session())).Run(ctx, lease.Session(), r.PathValue("ref"))
	})
}

func (h *Handler) appSchema(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return apps.NewSchemaAction(h.CacheFor(lease.Session())).Run(ctx, lease.Session(), r.PathValue("ref"))
	})
}

func (h *Handler) appHidden(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hidden bool `json:"hidden"`
	}

	if !httpx.DecodeBody(w, r, &in) {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		backup, err := (apps.VisibilityAction{}).Run(ctx, lease.Session(), r.PathValue("ref"), in.Hidden)
		if err != nil {
			return nil, err
		}

		return map[string]any{"hidden": in.Hidden, "backup": backup, noteKey: "Applied when the USB session ends; unplug the watch."}, nil
	})
}

func (h *Handler) appPending(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (apps.PendingRegistryAction{}).Run(ctx, lease.Session())
	})
}

func (h *Handler) appPendingCancel(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		if err := (apps.CancelRegistryAction{}).Run(ctx, lease.Session()); err != nil {
			return nil, err
		}

		return map[string]any{"ok": true}, nil
	})
}

func (h *Handler) appRemove(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		removed, err := apps.NewRemoveAction(h.CacheFor(lease.Session())).Run(ctx, lease.Session(), r.PathValue("ref"))
		if err != nil {
			return nil, err
		}

		return map[string]any{"removed": removed, noteKey: "The watch drops the app from its list when the USB session ends; unplug it."}, nil
	})
}

func (h *Handler) appsInstall(w http.ResponseWriter, r *http.Request) {
	data, ok := httpx.ReadBody(w, r, httpx.MaxUploadBytes)
	if !ok {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		p, err := apps.NewInstallPRGAction(h.CacheFor(lease.Session())).Run(ctx, lease.Session(), data)
		if err != nil {
			return nil, err
		}

		return map[string]any{"path": p, "bytes": len(data), noteKey: "The watch registers the app when the USB session ends; unplug it."}, nil
	})
}

func (h *Handler) appSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]any `json:"values"`
	}

	if !httpx.DecodeBody(w, r, &body) {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return apps.NewApplySettingsAction(h.CacheFor(lease.Session())).Run(ctx, lease.Session(), r.PathValue("ref"), body.Values)
	})
}
