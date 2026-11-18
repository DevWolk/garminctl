package desktop

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Proxy bridges the native WebView's private asset origin to the loopback API.
// Only requests from Wails' own origin are allowed to have their Origin rewritten.
func Proxy(backend *url.URL, script []byte) http.Handler {
	return proxyWithPageFailure(backend, script, nil)
}

var errNativePage = errors.New("native page unavailable")

func proxyWithPageFailure(backend *url.URL, script []byte, pageFailed func()) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(backend)
			r.Out.Host = backend.Host
			r.Out.Header.Del("Origin")
			r.Out.Header.Del("Sec-Fetch-Site")
		},
		ModifyResponse: injectDesktopScript,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if pageFailed != nil && (errors.Is(err, errNativePage) || pageRequest(r)) {
				pageFailed()
			}

			slog.Error("desktop page unavailable", "error", err)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
			http.Error(w, "Could not load the desktop page. Restart the garminctl backend and reopen the desktop app.", http.StatusBadGateway)
		},
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

		var seed [24]byte
		if _, err := rand.Read(seed[:]); err != nil {
			http.Error(w, "could not prepare desktop page", http.StatusInternalServerError)
			return
		}

		r = r.Clone(r.Context())
		r.Header.Set(httpx.PageNonceHeader, base64.StdEncoding.EncodeToString(seed[:]))

		// Native WebViews may supply a stream without an advertised length.
		// ReverseProxy treats zero as no body, so retain it as unknown length.
		if r.ContentLength == 0 && r.Body != nil && r.Body != http.NoBody {
			r.ContentLength = -1
			r.Header.Del("Content-Length")
		}

		proxy.ServeHTTP(w, r)
	})
}

// pageRequest reports a document navigation. Its response replaces the page
// and the script that answers close requests, unlike a fetch from a page that
// stays loaded.
func pageRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	if destination := r.Header.Get("Sec-Fetch-Dest"); destination != "" {
		return destination == "document"
	}

	return strings.Contains(r.Header.Get("Accept"), "text/html")
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
	acknowledged := response.Header.Get(httpx.PageNonceHeader)
	response.Header.Del(httpx.PageNonceHeader)

	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		return nil
	}

	nonce := response.Request.Header.Get(httpx.PageNonceHeader)
	if !httpx.ValidPageNonce(nonce) || acknowledged != nonce {
		return fmt.Errorf("%w: backend does not support native page nonce rendering", errNativePage)
	}

	response.Header.Set("Content-Security-Policy", "script-src 'nonce-"+nonce+"' 'strict-dynamic'; frame-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	response.Header.Set("Cache-Control", "no-store")
	response.Header.Del("ETag")
	response.Header.Del("Last-Modified")

	if response.Request.Method == http.MethodHead {
		// The upstream length omits the bridge appended to GET responses.
		response.Header.Del("Content-Length")
		response.ContentLength = -1

		return nil
	}

	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()

	if err != nil {
		return fmt.Errorf("%w: read desktop page: %w", errNativePage, err)
	}

	body, err = appendDesktopBridge(body, nonce)
	if err != nil {
		return errors.Join(errNativePage, err)
	}

	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))

	return nil
}

// Append only our own trusted node. Replacing a closing-tag string could insert
// the bridge and its nonce inside an attacker-controlled attribute or comment.
func appendDesktopBridge(body []byte, nonce string) ([]byte, error) {
	page, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse desktop page: %w", err)
	}

	for node := range page.Descendants() {
		if node.Type == html.ElementNode && node.DataAtom == atom.Body {
			node.AppendChild(&html.Node{
				Type: html.ElementNode, DataAtom: atom.Script, Data: "script",
				Attr: []html.Attribute{{Key: "type", Val: "module"}, {Key: "src", Val: "/desktop.js"}, {Key: "nonce", Val: nonce}},
			})

			break
		}
	}

	var rendered bytes.Buffer
	if err := html.Render(&rendered, page); err != nil {
		return nil, fmt.Errorf("render desktop page: %w", err)
	}

	return rendered.Bytes(), nil
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
