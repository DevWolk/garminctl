package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

func TestDecodeBodyKeepsSingleJSONAndLimitContracts(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, body string
		ok         bool
	}{
		{name: "object", body: `{"value":1}`, ok: true},
		{name: "trailing whitespace", body: "{} \n\t", ok: true},
		{name: "null", body: "null", ok: true},
		{name: "empty"},
		{name: "two values", body: "{} {}"},
		{name: "trailing data", body: "{}x"},
		{name: "invalid object", body: "{"},
		{name: "over one MiB", body: strings.Repeat(" ", 1<<20) + "{}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tt.body))
			response := httptest.NewRecorder()

			var decoded map[string]any
			if ok := DecodeBody(response, request, &decoded); ok != tt.ok {
				t.Fatalf("decode=%t expected=%t response=%s", ok, tt.ok, response.Body)
			}

			if !tt.ok {
				if response.Code != http.StatusBadRequest {
					t.Fatalf("JSON error status=%d, want the existing 400", response.Code)
				}

				var failure map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure["error"] == "" {
					t.Fatalf("error envelope=%s decode=%v", response.Body, err)
				}
			}
		})
	}
}

func TestReadBodyKeepsExactLimitAndOversizeStatus(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"abc", "abcd"} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/", strings.NewReader(body))
			response := httptest.NewRecorder()

			data, ok := ReadBody(response, request, 3)
			if len(body) == 3 {
				if !ok || string(data) != body {
					t.Fatalf("exact limit: data=%q ok=%t", data, ok)
				}

				return
			}

			if ok || data != nil || response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversize: data=%q ok=%t status=%d", data, ok, response.Code)
			}
		})
	}
}

type failedBody struct{}

func (failedBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReadBodyReportsInputFailure(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/", failedBody{})

	response := httptest.NewRecorder()
	if _, ok := ReadBody(response, request, 3); ok || response.Code != http.StatusBadRequest {
		t.Fatalf("read failure: ok=%t status=%d", ok, response.Code)
	}
}

func TestErrorStatusKeepsContextDeviceAndAdmissionPriority(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "deadline before disconnect", err: errors.Join(device.ErrDisconnected, context.DeadlineExceeded), status: http.StatusGatewayTimeout},
		{name: "cancelled", err: context.Canceled, status: http.StatusRequestTimeout},
		{name: "disconnected", err: device.ErrDisconnected, status: http.StatusServiceUnavailable},
		{name: "stopped", err: lifecycle.ErrStopped, status: http.StatusServiceUnavailable},
		{name: "maintenance", err: lifecycle.ErrMaintenance, status: http.StatusConflict},
		{name: "busy", err: lifecycle.ErrBusy, status: http.StatusConflict},
		{name: "missing file", err: device.ErrNotFound, status: http.StatusNotFound},
		{name: "invalid argument", err: device.ErrInvalidArgument, status: http.StatusBadRequest},
		{name: "fallback", err: io.ErrUnexpectedEOF, status: http.StatusTeapot},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ErrorStatus(tt.err, http.StatusTeapot); got != tt.status {
				t.Fatalf("status=%d, want %d", got, tt.status)
			}
		})
	}
}

func TestHTTPJSONFormatRemainsDistinctFromCLI(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	WriteJSON(response, http.StatusAccepted, map[string]any{"rows": []string{}})

	const expected = "{\n \"rows\": []\n}\n"
	if response.Code != http.StatusAccepted || response.Header().Get("Content-Type") != "application/json; charset=utf-8" || response.Body.String() != expected {
		t.Fatalf("HTTP JSON status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}
