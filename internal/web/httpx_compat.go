package web

import (
	"net/http"

	"github.com/DevWolk/garminctl/internal/ship/httpx"
)

// Temporary shell names keep routes stable until feature HTTP adapters migrate.
const (
	maxUploadBytes = httpx.MaxUploadBytes
)

func localOnly(addr string, next http.Handler) http.Handler  { return httpx.LocalOnly(addr, next) }
func writeJSON(w http.ResponseWriter, status int, value any) { httpx.WriteJSON(w, status, value) }
