//go:build desktop

package desktop

import (
	"context"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type v2WindowHost struct{ app *App }

func (h v2WindowHost) Emit(action string) {
	wailsruntime.EventsEmit(h.app.ctx, "desktop:action", action)
}
func (h v2WindowHost) Quit() { wailsruntime.Quit(h.app.ctx) }
func (h v2WindowHost) OpenExternal(raw string) error {
	wailsruntime.BrowserOpenURL(h.app.ctx, raw)
	return nil
}

func (h v2WindowHost) SaveFile(ctx context.Context, name string) (string, error) {
	return wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{DefaultFilename: name, Title: "Save file"})
}
