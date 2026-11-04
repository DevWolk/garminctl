package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

type blockedUpload struct {
	ctx     context.Context //nolint:containedctx // test input owns the request cancellation scope it observes
	entered chan struct{}
	once    sync.Once
}

func (b *blockedUpload) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.ctx.Done()

	return 0, b.ctx.Err()
}
func (*blockedUpload) Close() error { return nil }

func TestUploadBodyReservesAdmissionAndShutdownJoinsIt(t *testing.T) {
	t.Parallel()
	s := newServer(t.Context(), func(context.Context) (device.Device, error) {
		t.Error("body-only upload must not open a watch")
		return nil, errors.New("unexpected watch open")
	}, false)
	s.cloud = gc.New(filepath.Join(t.TempDir(), "account.json"))

	h, err := s.handler("127.0.0.1:8787")
	if err != nil {
		t.Fatal(err)
	}

	body := &blockedUpload{ctx: s.runtime.Context(), entered: make(chan struct{})}
	req := httptest.NewRequestWithContext(s.runtime.Context(), http.MethodPost, "http://127.0.0.1:8787/api/file?path=Music/test.mp3", body)

	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), req) }()

	select {
	case <-body.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload body did not start")
	}

	if resume, err := s.svc.Gate().TryMaintenance(t.Context()); !errors.Is(err, lifecycle.ErrBusy) {
		if resume != nil {
			resume()
		}

		t.Fatalf("maintenance started while reading upload: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := s.runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("shutdown returned before upload handler drained")
	}

	if err := s.mcp.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestActivitiesInitialOpenFailureRetainsStatusShape(t *testing.T) {
	t.Parallel()
	s := newServer(t.Context(), func(context.Context) (device.Device, error) { return nil, errors.New("synthetic missing watch") }, false)
	s.cloud = gc.New(filepath.Join(t.TempDir(), "account.json"))
	response := httptest.NewRecorder()

	mux := http.NewServeMux()
	for _, registrar := range s.features.HTTP {
		registrar.RegisterRoutes(mux)
	}

	mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health/activities", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, body=%s", response.Code, response.Body)
	}

	var state map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}

	if state["connected"] != false || state["error"] != "synthetic missing watch" {
		t.Fatalf("initial failure shape changed: %v", state)
	}

	if err := s.runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
