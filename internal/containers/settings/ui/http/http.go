// Package http adapts settings Actions to the existing local JSON API.
package http

import (
	"context"
	"net/http"

	"github.com/DevWolk/garminctl/internal/containers/settings"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Handler uses the backend's existing Manager and profile. The server shell
// applies Host/Origin guards, logging and static routing around these handlers.
type Handler struct {
	Manager *watchsession.Manager
	Profile *fit.Profile
}

// RegisterRoutes preserves the current settings read, apply and schema paths.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", h.read)
	mux.HandleFunc("POST /api/settings/apply", httpx.Admit(h.Manager.Gate(), h.apply))
	mux.HandleFunc("GET /api/schema", h.schema)
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		view, err := settings.NewReadAction(h.Profile).Run(ctx, lease.Session())
		if err != nil {
			return nil, err
		}

		return map[string]any{"rows": view.Rows()}, nil
	})
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Changes []settings.Change `json:"changes"`
	}

	if !httpx.DecodeBody(w, r, &body) {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return settings.NewApplyAction(h.Profile).Run(ctx, lease.Session(), body.Changes)
	})
}

func (h *Handler) schema(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(context.Context, *watchsession.Lease) (any, error) {
		return settings.NewSchemaAction(h.Profile).Run(r.URL.Query().Get("mesg"))
	})
}
