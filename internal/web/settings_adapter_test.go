package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
)

const settingsApplyPath = "/api/settings/apply"

func TestSettingsAdapterRemainsInsideShellGuards(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)
	for _, test := range []struct{ name, header, value string }{
		{name: "origin", header: "Origin", value: "https://evil.example"},
		{name: "fetch site", header: "Sec-Fetch-Site", value: "cross-site"},
		{name: "rebound host", header: "Host", value: "evil.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+settingsApplyPath, strings.NewReader(`{"changes":[]}`))
			if err != nil {
				t.Fatal(err)
			}

			if test.header == "Host" {
				req.Host = test.value
			} else {
				req.Header.Set(test.header, test.value)
			}

			response, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("settings mutation bypassed shell guard: %d", response.StatusCode)
			}
		})
	}
}

func TestSettingsAdapterPreservesBodyAndAcquisitionErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, method, path, body string
		status                   int
		opens                    int
	}{
		{name: "invalid JSON", method: http.MethodPost, path: settingsApplyPath, body: "{", status: http.StatusBadRequest},
		{name: "trailing JSON", method: http.MethodPost, path: settingsApplyPath, body: `{"changes":[]} {}`, status: http.StatusBadRequest},
		{name: "body limit", method: http.MethodPost, path: settingsApplyPath, body: `{"changes":[],"ignored":"` + strings.Repeat("x", httpx.MaxJSONBody) + `"}`, status: http.StatusBadRequest},
		{name: "read acquisition", method: http.MethodGet, path: "/api/settings", status: http.StatusServiceUnavailable, opens: 1},
		{name: "schema acquisition", method: http.MethodGet, path: "/api/schema?mesg=deviceSettings", status: http.StatusServiceUnavailable, opens: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			opens := 0
			s := newServer(t.Context(), func(context.Context) (device.Device, error) {
				opens++
				return nil, errors.New("synthetic missing watch")
			}, false)
			t.Cleanup(func() { closeSettingsTestRuntime(t, s) })

			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), test.method, test.path, strings.NewReader(test.body))

			mux := http.NewServeMux()
			for _, registrar := range s.features.HTTP {
				registrar.RegisterRoutes(mux)
			}

			mux.ServeHTTP(w, r)

			if w.Code != test.status || opens != test.opens {
				t.Fatalf("status=%d opens=%d body=%s", w.Code, opens, w.Body)
			}

			if test.opens != 0 && w.Body.String() != "{\n \"connected\": false,\n \"error\": \"synthetic missing watch\"\n}\n" {
				t.Fatalf("acquisition error envelope changed: %s", w.Body)
			}
		})
	}
}

type unreadSettingsBody struct{ read bool }

func (b *unreadSettingsBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*unreadSettingsBody) Close() error               { return nil }

func TestSettingsAdapterAdmitsBeforeReadingBody(t *testing.T) {
	t.Parallel()

	s := newServer(t.Context(), func(context.Context) (device.Device, error) {
		t.Error("maintenance must reject before watch acquisition")
		return nil, errors.New("unexpected watch open")
	}, false)
	t.Cleanup(func() { closeSettingsTestRuntime(t, s) })

	resume, err := s.svc.Gate().TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resume()

	mux := http.NewServeMux()
	for _, registrar := range s.features.HTTP {
		registrar.RegisterRoutes(mux)
	}

	body := new(unreadSettingsBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, settingsApplyPath, body))

	if w.Code != http.StatusConflict || body.read {
		t.Fatalf("maintenance status=%d body read=%v", w.Code, body.read)
	}
}

func closeSettingsTestRuntime(t *testing.T, s *Server) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := s.runtime.Close(ctx); err != nil {
		t.Error(err)
	}
}
