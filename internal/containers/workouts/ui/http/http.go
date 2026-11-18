// Package http adapts workout Actions to the existing guarded HTTP API.
package http

import (
	"context"
	"net/http"
	"strconv"

	"github.com/DevWolk/garminctl/internal/containers/workouts"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const noteKey = "note"

// Handler borrows the process Manager, account and workout-only delivery port.
type Handler struct {
	Manager     *watchsession.Manager
	Client      workouts.Account
	DeliveryFor func(*watchsession.Session) workouts.Delivery
}

// RegisterRoutes preserves all five workout routes and mutating admission.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workouts", h.workoutsList)
	mux.HandleFunc("POST /api/workouts", httpx.Admit(h.Manager.Gate(), h.workoutsCreate))
	mux.HandleFunc("POST /api/workouts/{id}/send", httpx.Admit(h.Manager.Gate(), h.workoutsSend))
	mux.HandleFunc("DELETE /api/workouts/{id}", httpx.Admit(h.Manager.Gate(), h.workoutsDelete))
	mux.HandleFunc("POST /api/workouts/preview", httpx.Admit(h.Manager.Gate(), h.workoutsPreview))
}

// ---------- workouts ----------

func (h *Handler) workoutsList(w http.ResponseWriter, r *http.Request) {
	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, _ *watchsession.Lease) (any, error) {
		return (workouts.ListAction{Client: h.Client}).Run(ctx, 0) // default page
	})
}

type workoutReq struct {
	Spec  workouts.WorkoutSpec `json:"spec"`
	Send  bool                 `json:"send"`  // queue it for the watch through Connect
	Local bool                 `json:"local"` // write the FIT straight to the watch instead of Connect
}

func (h *Handler) workoutsPreview(w http.ResponseWriter, r *http.Request) {
	var req workoutReq
	if !httpx.DecodeBody(w, r, &req) {
		return
	}

	doc, err := req.Spec.ConnectJSON()
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"spec": req.Spec, "connect": doc})
}

func (h *Handler) workoutsCreate(w http.ResponseWriter, r *http.Request) {
	var req workoutReq
	if !httpx.DecodeBody(w, r, &req) {
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		if req.Local {
			p, err := (workouts.WriteLocalAction{}).Run(ctx, lease.Session(), &req.Spec)
			if err != nil {
				return nil, err
			}

			return map[string]any{"path": p, noteKey: "Workout file written to GARMIN/NewFiles; unplug the watch and find it under Training > Workouts."}, nil
		}

		wk, err := (workouts.CreateAction{Client: h.Client}).Run(ctx, &req.Spec)
		if err != nil {
			return nil, err
		}

		res := map[string]any{"workout": wk}
		if !req.Send {
			res[noteKey] = "Saved in Garmin Connect."

			return res, nil
		}

		pulled, err := h.send(ctx, lease.Session(), wk.ID, wk.Name)
		if err != nil {
			res[noteKey] = "Saved in Garmin Connect, but sending to the watch failed: " + err.Error()

			return res, nil //nolint:nilerr // partial success: the workout is saved in Connect; the send failure is reported in the note
		}

		res["pulled"] = pulled
		res[noteKey] = sendNote(workouts.Written(pulled))

		return res, nil
	})
}

// sendNote describes what happened after Connect was asked to queue a workout.
func sendNote(written int, failure string) string {
	if written > 0 {
		return "Saved in Garmin Connect and written to the watch; unplug it to see the workout under Training > Workouts."
	}

	if failure != "" {
		return "Saved in Garmin Connect, but the workout was not written to the watch: " + failure + ". Sync later to try again."
	}

	return "Saved in Garmin Connect and queued for the watch; Sync later or let the phone deliver it."
}

func (h *Handler) workoutsSend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad workout id")
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, lease *watchsession.Lease) (any, error) {
		pulled, err := h.send(ctx, lease.Session(), id, r.URL.Query().Get("name"))
		if err != nil {
			return nil, err
		}

		return map[string]any{"pulled": pulled}, nil
	})
}

func (h *Handler) workoutsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad workout id")
		return
	}

	httpx.WithWatch(w, r, h.Manager, func(ctx context.Context, _ *watchsession.Lease) (any, error) {
		return map[string]any{"ok": true}, (workouts.DeleteAction{Client: h.Client}).Run(ctx, id)
	})
}

func (h *Handler) send(ctx context.Context, sess *watchsession.Session, id int64, name string) ([]workouts.PulledFile, error) {
	return (workouts.SendAction{Client: h.Client, Queue: h.DeliveryFor(sess)}).Run(ctx, id, name)
}
