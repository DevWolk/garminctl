//go:build desktop

package desktop

import (
	"html"
	"net/http"
)

// startupErrorHandler renders backend failure in the existing native host.
func startupErrorHandler(err error) http.Handler {
	page := "<!doctype html><meta charset=utf-8><title>garminctl</title><style>body{font:16px system-ui;padding:24px;line-height:1.6}p{overflow-wrap:anywhere}</style><h1>Could not open garminctl</h1><p>" + html.EscapeString(err.Error()) + "</p><p>Close this window after resolving the problem and open the application again.</p>"

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
}
