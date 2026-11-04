// Package http adapts catalog Actions to the local HTTP API.
package http

import (
	"context"
	"net/http"
	"strconv"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/containers/store"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Handler decorates network results only when the shared Manager is connected.
type Handler struct {
	Manager  *watchsession.Manager
	Registry func(*watchsession.Session) store.InstalledApps
}

// RegisterRoutes mounts the two existing read-only catalog endpoints.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/store", h.browse)
	mux.HandleFunc("GET /api/store/apps/{id}", h.detail)
}

func (h *Handler) withCurrent(w http.ResponseWriter, r *http.Request, body func(context.Context, *watchsession.Session) (any, error)) {
	out, err := httpx.WithCurrentWatch(r.Context(), h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		var sess *watchsession.Session
		if lease != nil {
			sess = lease.Session()
		}

		return body(ctx, sess)
	})
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}

func selectedPart(ctx context.Context, sess *watchsession.Session) string {
	if sess != nil {
		if dx, err := sess.DeviceXML(ctx); err == nil {
			return dx.PartNumber
		}
	}

	return ""
}

func (h *Handler) browse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	query := ciqstore.StoreQuery{Keywords: q.Get("q"), Type: q.Get("type"), Sort: q.Get("sort"), Page: page, PageSize: 0}

	h.withCurrent(w, r, func(ctx context.Context, sess *watchsession.Session) (any, error) {
		return store.NewBrowseAction(h.Registry(sess)).Run(ctx, selectedPart(ctx, sess), query)
	})
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	h.withCurrent(w, r, func(ctx context.Context, sess *watchsession.Session) (any, error) {
		return store.NewDetailAction(h.Registry(sess)).Run(ctx, selectedPart(ctx, sess), r.PathValue("id"))
	})
}
