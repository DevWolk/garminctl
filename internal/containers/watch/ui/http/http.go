// Package http adapts watch file operations and FIT decoding to the local API.
package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Handler uses the existing Manager; the shell applies origin guards and logging.
type Handler struct {
	Manager *watchsession.Manager
	Profile *fit.Profile
}

// RegisterRoutes retains the existing file and decode paths.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files", h.files)
	mux.HandleFunc("GET /api/file", h.fileGet)
	mux.HandleFunc("POST /api/file", httpx.Admit(h.Manager.Gate(), h.filePut))
	mux.HandleFunc("DELETE /api/file", httpx.Admit(h.Manager.Gate(), h.fileDelete))
	mux.HandleFunc("GET /api/decode", h.decode)
}

func (h *Handler) files(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return lease.Session().List(ctx, r.URL.Query().Get("path"))
	})
}

func (h *Handler) fileGet(w http.ResponseWriter, r *http.Request) {
	lease, err := h.Manager.Acquire(r.Context())
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}

	p := r.URL.Query().Get("path")

	data, err := lease.Session().ReadFile(lease.Context(), p)
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, h.Manager.DropAt(lease.Epoch()))
	}

	err = errors.Join(err, lease.Release())
	if err != nil {
		httpx.WriteErr(w, err)
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

func (h *Handler) filePut(w http.ResponseWriter, r *http.Request) {
	data, ok := httpx.ReadBody(w, r, httpx.MaxUploadBytes)
	if !ok {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		p := r.URL.Query().Get("path")
		if err := lease.Session().WriteFile(ctx, p, data); err != nil {
			return nil, err
		}

		return map[string]any{"ok": true, "path": p, "bytes": len(data)}, nil
	})
}

func (h *Handler) fileDelete(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		p := r.URL.Query().Get("path")
		if err := lease.Session().DeleteFile(ctx, p); err != nil {
			return nil, err
		}

		return map[string]any{"ok": true}, nil
	})
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return watch.NewDecodeAction(h.Profile).Run(ctx, lease.Session(), r.URL.Query().Get("path"))
	})
}
