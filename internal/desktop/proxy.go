package desktop

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// Proxy bridges the native WebView's private asset origin to the loopback API.
// Only requests from Wails' own origin are allowed to have their Origin rewritten.
func Proxy(backend *url.URL, script []byte) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(backend)
			r.Out.Host = backend.Host
			r.Out.Header.Del("Origin")
			r.Out.Header.Del("Sec-Fetch-Site")
		},
		ModifyResponse: injectDesktopScript,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !desktopOrigin(r) {
			http.Error(w, "forbidden: not a desktop request", http.StatusForbidden)
			return
		}

		if r.URL.Path == "/desktop.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(script)

			return
		}

		proxy.ServeHTTP(w, r)
	})
}

func desktopOrigin(r *http.Request) bool {
	var origin string

	switch r.Host {
	case "wails.localhost":
		origin = "http://wails.localhost"
	case "wails":
		origin = "wails://wails"
	case "localhost":
		origin = "wails://localhost"
	default:
		return false
	}

	return (r.Header.Get("Origin") == "" || r.Header.Get("Origin") == origin) && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func injectDesktopScript(response *http.Response) error {
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		return nil
	}

	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()

	if err != nil {
		return fmt.Errorf("read desktop page: %w", err)
	}

	body = bytes.Replace(body, []byte("</body>"), []byte("<script type=\"module\" src=\"/desktop.js\"></script></body>"), 1)
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))

	return nil
}

// DownloadURL accepts only the application's file export endpoints.
func DownloadURL(base *url.URL, relative string) (*url.URL, error) {
	u, err := url.Parse(relative)
	if err != nil || u.IsAbs() || u.Host != "" || (u.Path != "/api/file" && u.Path != "/api/decode") {
		return nil, fmt.Errorf("unsupported download URL: %q", relative)
	}

	return base.ResolveReference(u), nil
}

// ExternalURL permits ordinary web links, never local files or OS commands.
func ExternalURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("not a web link: %q", raw)
	}

	return u.String(), nil
}
