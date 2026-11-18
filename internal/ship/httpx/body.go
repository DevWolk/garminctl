package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

const (
	// MaxJSONBody retains the existing JSON request limit.
	MaxJSONBody = 1 << 20
	// MaxUploadBytes retains the ordinary file upload limit.
	MaxUploadBytes = 64 << 20
	// MaxTrackBytes retains the audio upload limit.
	MaxTrackBytes = 512 << 20
)

// WriteJSON writes the existing one-space-indented HTTP JSON representation.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	_ = enc.Encode(v)
}

// ReadBody reads an upload of at most limit bytes. A larger one is refused
// with 413; cutting it short would write a truncated file to the watch.
func ReadBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body larger than %d bytes", limit))
			return nil, false
		}

		WriteError(w, http.StatusBadRequest, err.Error())

		return nil, false
	}

	return data, true
}

// WriteError answers with {"error": msg}.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]any{"error": msg})
}

// WriteErr returns the common error envelope using the existing status mapping.
func WriteErr(w http.ResponseWriter, err error) {
	WriteError(w, ErrorStatus(err, http.StatusInternalServerError), err.Error())
}

// ErrorStatus maps context, device and admission errors, retaining the fallback.
func ErrorStatus(err error, fallback int) int {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout
	case errors.Is(err, device.ErrDisconnected):
		return http.StatusServiceUnavailable
	case errors.Is(err, lifecycle.ErrStopped):
		return http.StatusServiceUnavailable
	case errors.Is(err, lifecycle.ErrMaintenance), errors.Is(err, lifecycle.ErrBusy), errors.Is(err, device.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, device.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, device.ErrInvalidArgument):
		return http.StatusBadRequest
	default:
		return fallback
	}
}

// DecodeBody reads a JSON request body into v and answers 400 on failure.
// The body is capped at MaxJSONBody and must hold exactly one JSON value.
func DecodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return false
	}

	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, "trailing data after the JSON body")
		return false
	}

	return true
}
