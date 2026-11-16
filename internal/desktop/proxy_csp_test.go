package desktop

import (
	"encoding/base64"
	stdhtml "html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The shell marks its embedded template before any response-level injection.
// This header is an internal rendering contract, never a caller-selected nonce.
const pageNonceHeader = "X-Garminctl-Page-Nonce"

func TestProxyNativeScriptPolicy(t *testing.T) {
	t.Parallel()

	const suppliedNonce = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := r.Header.Get(pageNonceHeader)
		if nonce == "" || nonce == suppliedNonce {
			t.Error("proxy did not replace the caller's nonce")
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set(pageNonceHeader, nonce)
		_, _ = io.WriteString(w, `<!doctype html><html><body><script nonce="`+stdhtml.EscapeString(nonce)+`" src="/app.js"></script>`+
			`<script src="/untrusted.js"></script><div title='</body>'>untrusted text</div></body></html>`)
	}))
	t.Cleanup(backend.Close)

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	proxy := Proxy(target, nil)
	previous := ""

	for range 2 {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+windowsDesktopHost+"/settings", nil)
		request.Header.Set(pageNonceHeader, suppliedNonce)

		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("native page status=%d: %s", response.Code, response.Body)
		}

		scripts := nativePageScripts(t, response.Body.String())
		if len(scripts) != 3 {
			t.Fatalf("scripts=%v; bridge must be an element, even with </body> inside an attribute", scripts)
		}

		nonce := scripts["/app.js"]

		raw, err := base64.StdEncoding.DecodeString(nonce)
		if err != nil || len(raw) != 24 || nonce == previous || nonce == suppliedNonce {
			t.Fatalf("expected a fresh 192-bit nonce: %q, error=%v", nonce, err)
		}

		previous = nonce
		if scripts["/desktop.js"] != nonce || scripts["/untrusted.js"] != "" {
			t.Fatalf("trusted bridge and untrusted script nonce assignments: %v", scripts)
		}

		checkNativePagePolicy(t, response.Header(), nonce)
	}
}

func checkNativePagePolicy(t *testing.T, header http.Header, nonce string) {
	t.Helper()

	policy := header.Get("Content-Security-Policy")
	for _, directive := range []string{
		"script-src 'nonce-" + nonce + "' 'strict-dynamic'", "frame-src 'none'",
		"object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'",
	} {
		if !strings.Contains(policy, directive) {
			t.Errorf("native policy %q lacks %q", policy, directive)
		}
	}

	if strings.Contains(policy, "unsafe-inline") || strings.Contains(policy, "unsafe-eval") {
		t.Errorf("native policy allows untrusted execution: %q", policy)
	}

	if header.Get(pageNonceHeader) != "" || header.Get("Cache-Control") != "no-store" {
		t.Errorf("internal nonce header leaked or native HTML remained cacheable: %v", header)
	}
}

func TestProxyRefusesUnmarkedBackendHTML(t *testing.T) {
	t.Parallel()

	for _, echoed := range []string{"", "not-the-request-nonce"} {
		t.Run(echoed, func(t *testing.T) {
			t.Parallel()

			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set(pageNonceHeader, echoed)
				_, _ = io.WriteString(w, `<html><body><script src="/unmarked.js"></script></body></html>`)
			}))
			t.Cleanup(backend.Close)

			target, err := url.Parse(backend.URL)
			if err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+windowsDesktopHost+"/", nil)
			response := httptest.NewRecorder()
			Proxy(target, nil).ServeHTTP(response, request)

			if response.Code != http.StatusBadGateway || !strings.Contains(strings.ToLower(response.Body.String()), "restart") {
				t.Fatalf("old or mismatched backend needs an actionable error: status=%d, body=%s", response.Code, response.Body)
			}

			if strings.Contains(response.Body.String(), "/unmarked.js") || len(nativePageScripts(t, response.Body.String())) != 0 {
				t.Fatal("unmarked backend or native bridge executed on the error page")
			}
		})
	}
}

func nativePageScripts(t *testing.T, page string) map[string]string {
	t.Helper()

	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}

	scripts := map[string]string{}

	var visit func(*html.Node)

	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "script" {
			src, nonce := "", ""

			for _, attr := range node.Attr {
				switch attr.Key {
				case "src":
					src = attr.Val
				case "nonce":
					nonce = attr.Val
				}
			}

			scripts[src] = nonce
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)

	return scripts
}
