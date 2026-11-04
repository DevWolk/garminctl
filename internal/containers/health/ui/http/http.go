// Package http adapts health Actions and the shared decoder to the HTTP API.
package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const rangeDays = 14

// Handler borrows the same Manager/account policy and runtime decoder.
type Handler struct {
	Manager    *watchsession.Manager
	Profile    *fit.Profile
	Activities *health.ActivityCoordinator
	AccessFor  func(*watchsession.Session) health.AccountAccess
}
type connectionFailure struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

// RegisterRoutes retains six health routes and weight mutation admission.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health/activities", h.healthActivities)
	mux.HandleFunc("GET /api/health/activities/{name}", h.healthActivity)
	mux.HandleFunc("GET /api/health/daily", h.healthDaily)
	mux.HandleFunc("GET /api/health/day", h.healthDay)
	mux.HandleFunc("GET /api/health/extras", h.healthExtras)
	mux.HandleFunc("POST /api/health/weight", httpx.Admit(h.Manager.Gate(), h.healthWeight))
}

// ---------- health ----------

func (h *Handler) healthActivities(w http.ResponseWriter, r *http.Request) {
	out, err := h.Activities.Activities(r.Context(), r.URL.Query().Get("wait") == "1")
	if err != nil {
		if _, ok := errors.AsType[*health.WatchAcquireError](err); ok {
			httpx.WriteJSON(w, httpx.ErrorStatus(err, http.StatusServiceUnavailable), connectionFailure{Error: err.Error()})
			return
		}

		httpx.WriteErr(w, err)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) healthActivity(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (health.ReadActivityAction{Profile: h.Profile}).Run(ctx, lease.Session(), r.PathValue("name"), 0) // default track resolution
	})
}

func (h *Handler) healthDaily(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		if days > rangeDays {
			return (health.HistoryAction{Access: h.AccessFor(lease.Session())}).Run(ctx, days)
		}

		return (health.DailyAction{Access: h.AccessFor(lease.Session())}).Run(ctx, days)
	})
}

func (h *Handler) healthExtras(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (health.ExtrasAction{Access: h.AccessFor(lease.Session())}).Run(ctx)
	})
}

func (h *Handler) healthWeight(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kg float64 `json:"kg"`
	}

	if !httpx.DecodeBody(w, r, &in) {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		if err := (health.LogWeightAction{Access: h.AccessFor(lease.Session())}).Run(ctx, in.Kg, time.Time{}); err != nil {
			return nil, err
		}

		return map[string]any{"ok": true, "kg": in.Kg}, nil
	})
}

func (h *Handler) healthDay(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		return (health.DayAction{Access: h.AccessFor(lease.Session())}).Run(ctx, r.URL.Query().Get("date"))
	})
}
