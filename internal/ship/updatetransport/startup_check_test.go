package updatetransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStartupBackendProbeRequiresMatchingBoundedMetadata(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body string
		status     int
		want       bool
	}{
		{"ready", `{"application":"garminctl","desktopAPI":"1","version":"v1.2.3"}`, 200, true},
		{"mixed version", `{"application":"garminctl","desktopAPI":"1","version":"v0.9.0"}`, 200, false},
		{"foreign program", `{"application":"other","desktopAPI":"1","version":"v1.2.3"}`, 200, false},
		{"failed server", `{}`, 503, false},
		{"redirect", `{}`, 302, false},
		{"metadata exceeds limit", strings.Repeat(" ", 65<<10), 200, false},
		{"trailing JSON", `{} {}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var redirected atomic.Int32

			destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
			t.Cleanup(destination.Close)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/server" || r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("startup metadata request lost its read-only scope")
				}

				w.Header().Set("Location", destination.URL)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)

			err := probeStartupBackend(t.Context(), server.URL, startupTestVersion)
			if (err == nil) != tc.want {
				t.Fatalf("metadata probe=%v", err)
			}

			if redirected.Load() != 0 {
				t.Fatal("startup metadata followed a response-selected URL")
			}
		})
	}
}
