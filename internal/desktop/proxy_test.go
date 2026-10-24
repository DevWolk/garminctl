package desktop

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProxy(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			t.Error("desktop origin leaked to the local backend")
		}

		w.Header().Set("Content-Type", "text/html")
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
		{"Windows WebView", "wails.localhost", "http://wails.localhost", http.StatusOK},
		{"Linux WebView", "wails", "wails://wails", http.StatusOK},
		{"foreign host", "example.com", "", http.StatusForbidden},
		{"foreign origin", "wails.localhost", "https://example.com", http.StatusForbidden},
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

			if test.status == http.StatusOK && !strings.Contains(response.Body.String(), `<script src="/desktop.js"></script>`) {
				t.Fatal("native bridge was not injected")
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
