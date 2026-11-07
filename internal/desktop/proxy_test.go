package desktop

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/httpx"
)

const (
	windowsDesktopHost   = "wails.localhost"
	windowsDesktopOrigin = "http://wails.localhost"
	linuxDesktopHost     = "localhost"
	linuxDesktopOrigin   = "wails://localhost"
)

func TestProxy(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			t.Error("desktop origin leaked to the local backend")
		}

		w.Header().Set("Content-Type", "text/html")
		w.Header().Set(httpx.PageNonceHeader, r.Header.Get(httpx.PageNonceHeader))
		_, _ = w.Write([]byte("<html><body>garminctl</body></html>"))
	}))
	t.Cleanup(backend.Close)

	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	proxy := Proxy(u, []byte("// native bridge"))

	for _, test := range []struct {
		name, host, origin string
		status             int
	}{
		{"Windows WebView", windowsDesktopHost, windowsDesktopOrigin, http.StatusOK},
		{"Linux WebView", "wails", "wails://wails", http.StatusOK},
		{"foreign host", "example.com", "", http.StatusForbidden},
		{"foreign origin", windowsDesktopHost, "https://example.com", http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+test.host+"/apps/Easy+", nil)
			req.Header.Set("Origin", test.origin)

			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, req)

			if response.Code != test.status {
				t.Fatalf("status %d, want %d", response.Code, test.status)
			}

			if test.status == http.StatusOK {
				if nonce, ok := nativePageScripts(t, response.Body.String())["/desktop.js"]; !ok || !httpx.ValidPageNonce(nonce) {
					t.Fatal("native bridge was not injected with its nonce")
				}
			}
		})
	}
}

func TestProxyPreservesNativeBodiesWithoutAdvertisedLength(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("bounded") {
			r.Body = http.MaxBytesReader(w, r.Body, 4)
		}

		data, err := io.ReadAll(r.Body)
		if err != nil {
			if _, limited := errors.AsType[*http.MaxBytesError](err); !limited {
				t.Error(err)
			}

			w.WriteHeader(http.StatusRequestEntityTooLarge)

			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(backend.Close)

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	for _, native := range []struct {
		name, host, origin string
		bounded            bool
	}{
		{"Windows", windowsDesktopHost, windowsDesktopOrigin, false},
		{"Linux", linuxDesktopHost, linuxDesktopOrigin, false},
		{"Windows body limit", windowsDesktopHost, windowsDesktopOrigin, true},
		{"Linux body limit", linuxDesktopHost, linuxDesktopOrigin, true},
	} {
		t.Run(native.name, func(t *testing.T) {
			t.Parallel()

			const body = `{"changes":[{"field":"weight","value":80}]}`

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+native.host+"/api/settings", nil)
			req.Body = io.NopCloser(strings.NewReader(body))
			req.ContentLength = 0
			req.Header.Set("Content-Length", "0")
			req.Header.Set("Origin", native.origin)
			req.Header.Set("Content-Type", "application/json")

			if native.bounded {
				req.URL.RawQuery = "bounded=true"
			}

			response := httptest.NewRecorder()
			Proxy(target, nil).ServeHTTP(response, req)

			wantBody, wantStatus := body, http.StatusOK
			if native.bounded {
				wantBody, wantStatus = "", http.StatusRequestEntityTooLarge
			}

			if response.Code != wantStatus || response.Body.String() != wantBody {
				t.Fatalf("native forwarding: status=%d body=%q, want %d %q", response.Code, response.Body.String(), wantStatus, wantBody)
			}
		})
	}
}

func TestDownloadURL(t *testing.T) {
	t.Parallel()

	base := &url.URL{Scheme: "http", Host: "127.0.0.1:8787"}

	for _, test := range []struct {
		name, path string
		valid      bool
	}{
		{"watch file", "/api/file?path=GARMIN%2FSettings%2FSettings.fit", true},
		{"decoded file", "/api/decode?path=GARMIN%2FActivity%2Frun.fit", true},
		{"remote host", "https://example.com/api/file", false},
		{"protocol relative", "//example.com/api/file", false},
		{"other endpoint", "/api/settings/apply", false},
		{"local file", "file:///etc/passwd", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := DownloadURL(base, test.path)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}

			if err == nil && got.Host != base.Host {
				t.Fatal("download escaped the backend")
			}
		})
	}
}
