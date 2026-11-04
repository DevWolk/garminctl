package web

import (
	"net/http"

	"github.com/DevWolk/garminctl/internal/ship/httpx"
)

// Temporary shell names keep routes stable until feature HTTP adapters migrate.
const (
	maxJSONBody    = httpx.MaxJSONBody
	maxUploadBytes = httpx.MaxUploadBytes
)

func localOnly(addr string, next http.Handler) http.Handler  { return httpx.LocalOnly(addr, next) }
func writeJSON(w http.ResponseWriter, status int, value any) { httpx.WriteJSON(w, status, value) }

func writeError(w http.ResponseWriter, status int, message string) {
	httpx.WriteError(w, status, message)
}
func writeErr(w http.ResponseWriter, err error) { httpx.WriteErr(w, err) }
func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	return httpx.DecodeBody(w, r, value)
}
