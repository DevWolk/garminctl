// Package http adapts local audio Actions to guarded HTTP routes.
package http

import (
	"context"
	"net/http"

	"github.com/DevWolk/garminctl/internal/containers/music"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Handler uses the backend's existing watch Manager.
type Handler struct{ Manager *watchsession.Manager }

// RegisterRoutes keeps media upload limits, mutation admission and JSON guards.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/music", h.list)
	mux.HandleFunc("POST /api/music", httpx.Admit(h.Manager.Gate(), h.add))
	mux.HandleFunc("DELETE /api/music", httpx.Admit(h.Manager.Gate(), h.remove))
	mux.HandleFunc("PUT /api/music/playlists/{name}", httpx.Admit(h.Manager.Gate(), h.playlist))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (music.ListAction{}).Run(ctx, lease.Session())
	})
}

func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	data, ok := httpx.ReadBody(w, r, httpx.MaxTrackBytes)
	if !ok {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (music.AddTrackAction{}).Run(ctx, lease.Session(), r.URL.Query().Get("folder"), r.URL.Query().Get("name"), data)
	})
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return map[string]any{"ok": true}, (music.RemoveTrackAction{}).Run(ctx, lease.Session(), r.URL.Query().Get("path"))
	})
}

func (h *Handler) playlist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tracks []string `json:"tracks"`
	}
	if !httpx.DecodeBody(w, r, &body) {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (music.WritePlaylistAction{}).Run(ctx, lease.Session(), r.PathValue("name"), body.Tracks)
	})
}
