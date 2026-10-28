// Package httpx provides shared local HTTP guards, request bounds and responses.
// Feature policy and native WebView origin handling stay in their adapters.
package httpx

import (
	"net"
	"net/http"
	"strings"
)

// LocalOnly refuses requests that did not come from a page this server
// served. The server has no authentication: anything that can reach it can
// write to the watch, so a web page from elsewhere must not be able to call
// the API through the browser (cross-site requests, DNS rebinding).
func LocalOnly(addr string, next http.Handler) http.Handler {
	listenHost, port, err := net.SplitHostPort(addr)
	if err != nil {
		listenHost, port = "", ""
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, listenHost, port) {
			http.Error(w, "forbidden: unexpected Host", http.StatusForbidden)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
			return
		}

		if crossSiteCall(r) {
			http.Error(w, "forbidden: cross-site request", http.StatusForbidden)
			return
		}

		// Another site must not frame the UI and click its buttons.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")

		next.ServeHTTP(w, r)
	})
}

// crossSiteCall reports a browser request made by another site's page. A
// plain navigation into the UI (the user following a link) is fine: it
// carries no body and only renders the page. A navigation straight to an
// API path is not: some GET endpoints act (connect to the watch, wait for
// a file, send the device XML to Garmin), and a link is enough to fire
// them. Everything else from another site, fetches as well as form posts,
// is refused.
func crossSiteCall(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
	default:
		return false
	}

	isNavigation := r.Header.Get("Sec-Fetch-Mode") == "navigate"
	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead
	isPage := !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/mcp"

	return !isNavigation || !isRead || !isPage
}

// hostAllowed accepts a Host header that names this server on its port:
// localhost, any IP literal (the address the user typed, on the LAN too) or
// the name the server was told to listen on. DNS rebinding always arrives
// under some other name, which is what this refuses.
func hostAllowed(host, listenHost, port string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p = host, ""
	}

	if port != "" && p != port {
		return false
	}

	h = strings.Trim(h, "[]")
	if h == "localhost" || (listenHost != "" && strings.EqualFold(h, strings.Trim(listenHost, "[]"))) {
		return true
	}

	return net.ParseIP(h) != nil
}

func sameOrigin(origin, host string) bool {
	return origin == "http://"+host
}
