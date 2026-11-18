package desktop

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func TestStartBackend(t *testing.T) {
	t.Parallel()
	root := testdump.Root(t)
	open := func(_ context.Context) (device.Device, error) { return device.NewDirFS(root) }
	serve := func(ctx context.Context, listener net.Listener) error {
		return app.ServeListener(ctx, listener, open, false, "test-version")
	}

	backend, err := StartBackend(t.Context(), "127.0.0.1:0", serve)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})

	other, err := StartBackend(t.Context(), backend.URL.Host, serve)
	if err != nil {
		t.Fatal(err)
	}

	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL.String()+"/api/server", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("closing a reused backend stopped the original: %v", err)
	}
	defer response.Body.Close()

	var info map[string]string
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}

	if info["application"] != "garminctl" || info["version"] != "test-version" {
		t.Fatalf("server identity: %v", info)
	}

	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartBackendRefusesUnrelatedServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"application":"another-app"}`))
	}))
	t.Cleanup(server.Close)

	_, err := StartBackend(t.Context(), strings.TrimPrefix(server.URL, "http://"), nil)
	if err == nil {
		t.Fatal("accepted an unrelated service as garminctl")
	}
}

func TestStartBackendRejectsNonLoopback(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"0.0.0.0:8787", "192.0.2.1:8787", "example.com:8787", "bad-address"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()

			if _, err := StartBackend(t.Context(), address, nil); err == nil {
				t.Fatal("accepted a non-loopback address")
			}
		})
	}
}

func TestStartPrivateBackendNeverReusesARunningServer(t *testing.T) {
	t.Parallel()
	root := testdump.Root(t)
	open := func(_ context.Context) (device.Device, error) { return device.NewDirFS(root) }
	serveAs := func(version string) func(context.Context, net.Listener) error {
		return func(ctx context.Context, listener net.Listener) error {
			return app.ServeListener(ctx, listener, open, false, version)
		}
	}

	running, err := StartBackend(t.Context(), "127.0.0.1:0", serveAs("autostart"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := running.Close(); err != nil {
			t.Error(err)
		}
	})

	private, err := StartPrivateBackend(t.Context(), running.URL.Host, serveAs("dump"))
	if err != nil {
		t.Fatal(err)
	}

	if private.URL.Host == running.URL.Host {
		t.Fatalf("reused the server at %s", running.URL.Host)
	}

	if version := serverVersion(t, private.URL.String()); version != "dump" {
		t.Fatalf("window would talk to the %q server", version)
	}

	if err := private.Close(); err != nil {
		t.Fatal(err)
	}

	if version := serverVersion(t, running.URL.String()); version != "autostart" {
		t.Fatalf("closing the private server changed the running one: %q", version)
	}
}

func serverVersion(t *testing.T, base string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/api/server", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	var info map[string]string
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}

	return info["version"]
}
