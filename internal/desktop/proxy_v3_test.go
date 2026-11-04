package desktop

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestNativeV3ProxyKeepsStrictOrigins(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			t.Error("private-origin headers leaked")
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	proxy := Proxy(target, nil)

	t.Run("origins", func(t *testing.T) {
		for _, tc := range []struct {
			name, host, origin, site string
			want                     int
		}{
			{"Windows private origin", windowsDesktopHost, windowsDesktopOrigin, "same-origin", 200},
			{"legacy Linux private origin", "wails", "wails://wails", "same-origin", 200},
			{"Linux private document", linuxDesktopHost, "", "", 200},
			{"Linux private API", linuxDesktopHost, linuxDesktopOrigin, "same-origin", 200},
			{"foreign_host", "evil.example", "", "", 403},
			{"localhost_foreign_origin", linuxDesktopHost, "https://evil.example", "", 403},
			{"localhost_cross_site", linuxDesktopHost, linuxDesktopOrigin, "cross-site", 403},
			{"windows_cross_site", windowsDesktopHost, windowsDesktopOrigin, "cross-site", 403},
			{"localhost_port_not_allowed", "localhost:8787", linuxDesktopOrigin, "", 403},
			{"loopback_ip_not_allowed", "127.0.0.1", "", "", 403},
			{"mixed_private_origins", windowsDesktopHost, linuxDesktopOrigin, "", 403},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequestWithContext(t.Context(), "GET", "http://"+tc.host+"/api/server", nil)
				req.Host = tc.host
				req.Header.Set("Origin", tc.origin)
				req.Header.Set("Sec-Fetch-Site", tc.site)

				rec := httptest.NewRecorder()
				proxy.ServeHTTP(rec, req)

				if rec.Code != tc.want {
					t.Fatalf("got %d want %d", rec.Code, tc.want)
				}
			})
		}
	})

	if calls.Load() != 4 {
		t.Fatalf("upstream calls %d want4", calls.Load())
	}
}
