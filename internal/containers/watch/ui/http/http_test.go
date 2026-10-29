package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func testFileHandler(t *testing.T) (*watchsession.Manager, *http.ServeMux) {
	t.Helper()

	manager := watchsession.NewManager(func(context.Context) (device.Device, error) { return nil, errors.New("synthetic missing watch") })

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := manager.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	mux := http.NewServeMux()
	(&Handler{Manager: manager}).RegisterRoutes(mux)

	return manager, mux
}

func TestFileAcquisitionRetainsJSONAndDownloadErrorContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, path string
		status     int
		body       string
	}{
		{name: "JSON", path: "/api/files", status: http.StatusServiceUnavailable, body: "{\n \"connected\": false,\n \"error\": \"synthetic missing watch\"\n}\n"},
		{name: "download", path: "/api/file?path=any", status: http.StatusInternalServerError, body: "{\n \"error\": \"synthetic missing watch\"\n}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, mux := testFileHandler(t)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil))

			if w.Code != test.status || w.Body.String() != test.body {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

type unreadFileBody struct{ read bool }

func (b *unreadFileBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*unreadFileBody) Close() error               { return nil }

func TestFileMaintenanceRejectsBeforeReadingBody(t *testing.T) {
	t.Parallel()

	manager, mux := testFileHandler(t)

	resume, err := manager.Gate().TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resume()

	body := new(unreadFileBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/file?path=any", body))

	if w.Code != http.StatusConflict || body.read {
		t.Fatalf("maintenance status=%d body read=%v", w.Code, body.read)
	}
}
