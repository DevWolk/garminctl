package app_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/application"
)

type inventoryLocalFixture struct{}

func (inventoryLocalFixture) Inspect(context.Context) (application.LocalInventory, error) {
	return application.LocalInventory{OS: "linux", Arch: "amd64", Desktop: application.BinaryObservation{Path: "/fixture/garmin-desktop"}, CLI: application.BinaryObservation{Path: "/fixture/garminctl", Version: "v1.2.1"}}, nil
}

func TestApplicationInventoryDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/server" {
			t.Errorf("followed redirect to %s", r.URL.Path)
		}

		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	action, err := app.NewApplicationInventory(app.ApplicationInventoryConfig{BackendURL: endpoint, LocalSource: inventoryLocalFixture{}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := action.Run(t.Context())
	if err != nil || result == nil || result.Backend.Error == "" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestApplicationInventoryCancelsBackendRequest(t *testing.T) {
	t.Parallel()

	endpoint := &url.URL{Scheme: "http", Host: "[::1]:1234"}
	client := &http.Client{Transport: releaseRoundTrip(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Error("metadata probe is not bounded")
		}

		<-r.Context().Done()

		return nil, r.Context().Err()
	})}

	action, err := app.NewApplicationInventory(app.ApplicationInventoryConfig{BackendURL: endpoint, HTTPClient: client, LocalSource: inventoryLocalFixture{}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	t.Cleanup(cancel)

	if result, err := action.Run(ctx); result != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestApplicationInventoryReadsSeparateLoopbackBackendVersion(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		if r.Method != http.MethodGet || r.URL.Path != "/api/server" {
			t.Errorf("unexpected metadata request %s %s", r.Method, r.URL)
		}

		_, _ = io.WriteString(w, `{"application":"garminctl","desktopAPI":"1","version":"v1.2.2","extra":"existing metadata may grow"}`)
	}))
	t.Cleanup(server.Close)

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	action, err := app.NewApplicationInventory(app.ApplicationInventoryConfig{DesktopVersion: "v1.2.3", BackendURL: endpoint, BackendOwned: false, LocalSource: inventoryLocalFixture{}})
	if err != nil {
		t.Fatal(err)
	}

	if calls != 0 {
		t.Fatal("constructor performed IO")
	}

	result, err := action.Run(t.Context())
	if err != nil || result == nil {
		t.Fatalf("result=%+v error=%v", result, err)
	}

	if calls != 1 || result.Desktop.Version != "v1.2.3" || result.CLI.Version != "v1.2.1" || result.Backend.Version != "v1.2.2" || result.Backend.Owned || result.VersionAgreement != "different" {
		t.Fatalf("roles=%+v calls=%d", result, calls)
	}
}

func TestApplicationInventoryRejectsUnsafeEndpointBeforeIO(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"https://example.org", "http://localhost:12", "http://192.0.2.1:12", "file:///tmp/data", "http://user@127.0.0.1:12", "http://127.0.0.1:12/?key=value", "http://127.0.0.1:12/unexpected", "http://127.0.0.1:12/#fragment"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()

			endpoint, err := url.Parse(address)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = app.NewApplicationInventory(app.ApplicationInventoryConfig{BackendURL: endpoint, LocalSource: inventoryLocalFixture{}}); err == nil {
				t.Fatal("unsafe endpoint accepted")
			}
		})
	}
}

func TestApplicationInventoryBoundsAndValidatesBackendMetadata(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{"application":"other","desktopAPI":"1","version":"v1"}`, `{"application":"garminctl","desktopAPI":"2","version":"v1"}`, `{"application":"garminctl","desktopAPI":"1"}`, `{"application":"garminctl","desktopAPI":"1","version":"v1"} {}`, `{"application":"garminctl","desktopAPI":"1","version":"v1"}` + strings.Repeat(" ", 65<<10)} {
		t.Run(body[:min(len(body), 45)], func(t *testing.T) {
			t.Parallel()

			endpoint := &url.URL{Scheme: "http", Host: "127.0.0.1:1234"}
			client := &http.Client{Transport: releaseRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}

			action, err := app.NewApplicationInventory(app.ApplicationInventoryConfig{BackendURL: endpoint, BackendOwned: true, HTTPClient: client, LocalSource: inventoryLocalFixture{}})
			if err != nil {
				t.Fatal(err)
			}

			result, err := action.Run(t.Context())
			if err != nil || result == nil || result.Backend.Error == "" || !result.Backend.Owned || result.Backend.Version != "" || result.CLI.Version == "" {
				t.Fatalf("partial=%+v error=%v", result, err)
			}
		})
	}
}
