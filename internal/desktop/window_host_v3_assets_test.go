//go:build desktop

package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const nativeTestRuntimePath = "/wails/runtime"

func TestNativeAssetGuardPrecedesRuntimeDispatch(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, host, origin, site, method, path string
		want                                   int
	}{
		{name: "Windows runtime", host: windowsDesktopHost, origin: windowsDesktopOrigin, method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusUnprocessableEntity},
		{name: "Linux runtime", host: linuxDesktopHost, origin: linuxDesktopOrigin, method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusUnprocessableEntity},
		{name: "legacy Linux runtime", host: "wails", origin: "wails://wails", method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusUnprocessableEntity},
		{name: "native request without Origin", host: windowsDesktopHost, method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusUnprocessableEntity},
		{name: "foreign Origin", host: windowsDesktopHost, origin: "https://evil.example", method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusForbidden},
		{name: "foreign Host", host: "evil.example", method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusForbidden},
		{name: "null Origin", host: windowsDesktopHost, origin: "null", method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusForbidden},
		{name: "mixed private origins", host: windowsDesktopHost, origin: linuxDesktopOrigin, method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusForbidden},
		{name: "Host with port", host: "localhost:8787", origin: linuxDesktopOrigin, method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusForbidden},
		{name: "cross-site request", host: windowsDesktopHost, origin: windowsDesktopOrigin, site: "cross-site", method: http.MethodPost, path: nativeTestRuntimePath, want: http.StatusForbidden},
		{name: "GET runtime", host: windowsDesktopHost, method: http.MethodGet, path: nativeTestRuntimePath, want: http.StatusMethodNotAllowed},
		{name: "GET query fallback", host: windowsDesktopHost, method: http.MethodGet, path: nativeTestRuntimePath + "?object=invalid&method=invalid", want: http.StatusMethodNotAllowed},
		{name: "HEAD runtime", host: windowsDesktopHost, method: http.MethodHead, path: nativeTestRuntimePath, want: http.StatusMethodNotAllowed},
		{name: "PUT runtime", host: windowsDesktopHost, method: http.MethodPut, path: nativeTestRuntimePath, want: http.StatusMethodNotAllowed},
		{name: "OPTIONS runtime", host: windowsDesktopHost, method: http.MethodOptions, path: nativeTestRuntimePath, want: http.StatusMethodNotAllowed},
		{name: "runtime module", host: windowsDesktopHost, method: http.MethodGet, path: "/wails/runtime.js", want: http.StatusNoContent},
		{name: "transport module", host: linuxDesktopHost, method: http.MethodGet, path: "/wails/transport.js", want: http.StatusNoContent},
		{name: "desktop module", host: windowsDesktopHost, method: http.MethodGet, path: "/desktop.js", want: http.StatusNoContent},
		{name: "foreign module request", host: windowsDesktopHost, origin: "https://evil.example", method: http.MethodGet, path: "/wails/runtime.js", want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assetsCalled := false
			options := nativeAssetOptions(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				assetsCalled = true

				w.WriteHeader(http.StatusNoContent)
			}))
			// Match the pinned host's ordering without constructing a native window.
			handler := application.NewHTTPTransport().Handler()(options.Handler)
			if options.Middleware != nil {
				handler = options.Middleware(handler)
			}

			body := ""
			if test.method == http.MethodPost {
				body = "{}"
			}

			req := httptest.NewRequestWithContext(t.Context(), test.method, "http://"+test.host+test.path, strings.NewReader(body))
			req.Header.Set("Origin", test.origin)
			req.Header.Set("Sec-Fetch-Site", test.site)

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)

			if response.Code != test.want || assetsCalled != (test.want == http.StatusNoContent) {
				t.Fatalf("status=%d assets=%t, want status=%d", response.Code, assetsCalled, test.want)
			}

			if test.want == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodPost {
				t.Fatalf("Allow=%q, want POST", response.Header().Get("Allow"))
			}
		})
	}
}
