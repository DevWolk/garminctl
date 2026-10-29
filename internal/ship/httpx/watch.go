package httpx

import (
	"context"
	"errors"
	"net/http"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// RouteRegistrar mounts feature API handlers before the shell applies guards.
type RouteRegistrar interface {
	RegisterRoutes(*http.ServeMux)
}

// WatchLease is the lifetime contract for Session and compatibility leases.
type WatchLease interface {
	Context() context.Context
	Epoch() watchsession.ConnectionEpoch
	Release() error
}

// WatchOwner admits operation groups and retires a matching broken connection.
type WatchOwner[L WatchLease] interface {
	Acquire(context.Context) (L, error)
	DropAt(watchsession.ConnectionEpoch) error
}

// WithWatch scopes a JSON response to one watch lease. The initial acquisition
// error retains the connection-state envelope; body/cleanup errors use WriteErr.
// body must use its supplied context and must not retain or release the lease.
func WithWatch[L WatchLease](w http.ResponseWriter, r *http.Request, owner WatchOwner[L], body func(context.Context, L) (any, error)) {
	lease, err := owner.Acquire(r.Context())
	if err != nil {
		WriteJSON(w, ErrorStatus(err, http.StatusServiceUnavailable), struct {
			Connected bool   `json:"connected"`
			Error     string `json:"error,omitempty"`
		}{Error: err.Error()})

		return
	}

	value, err := body(lease.Context(), lease)
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, owner.DropAt(lease.Epoch()))
	}

	err = errors.Join(err, lease.Release())
	if err != nil {
		WriteErr(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, value)
}

// Admit reserves a mutating request before body or account-store work, without
// opening a watch or serializing network work through a watch lease.
func Admit(gate *lifecycle.Gate, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		release, err := gate.Enter(r.Context())
		if err != nil {
			WriteErr(w, err)
			return
		}
		defer release()

		next(w, r)
	}
}
