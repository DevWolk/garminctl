//go:build desktop

package desktop

import (
	"html"
	"net/http"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// ShowError displays a startup failure even in a Windows GUI build without a console.
func ShowError(err error) error {
	page := "<!doctype html><meta charset=utf-8><title>garminctl</title><style>body{font:16px system-ui;padding:24px;line-height:1.6}p{overflow-wrap:anywhere}</style><h1>Could not open garminctl</h1><p>" + html.EscapeString(err.Error()) + "</p><p>Close this window after resolving the problem and open the application again.</p>"

	return wails.Run(&options.App{
		Title: "garminctl — startup error", Width: 640, Height: 360,
		AssetServer: &assetserver.Options{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		})},
	})
}
