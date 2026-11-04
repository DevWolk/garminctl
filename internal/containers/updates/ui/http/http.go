// Package http adapts watch update Actions to the local API.
package http

import (
	"context"
	"net/http"

	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Handler shares the backend's watch and current-epoch feature cache.
type Handler struct {
	Manager       *watchsession.Manager
	Config        func() updates.Config
	Registry      func(*watchsession.Session) updates.InstalledApps
	InvalidateFor func(*watchsession.Session) func(uint64)
}

// RegisterRoutes preserves existing paths and install admission/body protection.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/updates/software", h.softwareUpdates)
	mux.HandleFunc("POST /api/updates/software/install", httpx.Admit(h.Manager.Gate(), h.install))
	mux.HandleFunc("GET /api/updates/apps", h.appUpdates)
	mux.HandleFunc("GET /api/software", h.inventory)
}

func (h *Handler) softwareUpdates(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return updates.NewCheckSoftwareAction(h.Config()).Run(ctx, lease.Session())
	})
}

func (h *Handler) install(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PartNumber string `json:"partNumber"`
	}
	if !httpx.DecodeBody(w, r, &body) {
		return
	}

	if body.PartNumber == "" {
		httpx.WriteError(w, http.StatusBadRequest, "partNumber required")
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return updates.NewInstallSoftwareAction(h.Config(), h.InvalidateFor(lease.Session())).Run(ctx, lease.Session(), body.PartNumber)
	})
}

func (h *Handler) appUpdates(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return updates.NewCheckAppsAction(h.Registry(lease.Session())).Run(ctx)
	})
}

func (h *Handler) inventory(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (updates.InventoryAction{}).Run(ctx, lease.Session())
	})
}
