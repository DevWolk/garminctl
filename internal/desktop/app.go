//go:build desktop

package desktop

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

//go:embed desktop.js
var desktopScript []byte

//go:embed icon.png
var appIcon []byte

// App is the small native bridge: lifecycle, browser links and file dialogs.
// Watch operations remain in the shared local API and MCP server.
type App struct {
	backend     *Backend
	host        windowHost
	ready       atomic.Bool
	closing     atomic.Bool
	operations  nativeOperations
	onReady     func(context.Context, Scope)
	readyNotice sync.Once
}

func (a *App) startup(ctx context.Context) {
	a.operations.startup(ctx)
}

func (a *App) emit(action string) { a.host.Emit(action) }

func (a *App) beforeClose(_ context.Context) bool {
	if a.closing.Load() || !a.ready.Load() {
		return false
	}

	a.emit("close")

	return true
}

// Ready enables the frontend's unsaved-edit guard after the page has loaded.
func (a *App) Ready() {
	a.ready.Store(true)
	slog.Info("desktop window ready")

	if a.onReady != nil {
		a.readyNotice.Do(func() {
			ctx, release, err := a.operations.begin()
			if err != nil {
				return
			}
			defer release()

			a.onReady(ctx, serviceScope{app: a})
		})
	}
}

// FinishClose is called after the frontend confirms pending edits may be lost.
func (a *App) FinishClose() {
	a.closing.Store(true)
	a.operations.cancel()
	a.host.Quit()
}

// OpenExternal opens an ordinary link in the user's default web browser.
func (a *App) OpenExternal(raw string) error {
	u, err := ExternalURL(raw)
	if err != nil {
		return err
	}

	return a.host.OpenExternal(u)
}

// Download lets the user choose where a watch file or decoded JSON is saved.
func (a *App) Download(relative, name string) error {
	ctx, err := a.operations.windowContext()
	if err != nil {
		return err
	}

	if a.backend == nil || a.backend.URL == nil {
		return errors.New("desktop backend is not ready")
	}

	u, err := DownloadURL(a.backend.URL, relative)
	if err != nil {
		return err
	}

	path, err := a.host.SaveFile(ctx, filepath.Base(name))
	if err != nil || path == "" {
		return err
	}

	if _, err := os.Stat(path); err == nil {
		replace, err := a.host.ConfirmReplace(ctx, path)
		if err != nil || !replace {
			return err
		}
	}

	return a.operations.save(u, path)
}

// shutdown joins native operations and only the backend this process owns. It also runs
// from the native hook, before framework exit paths can bypass Run's caller.
func (a *App) shutdown() error {
	a.operations.close()

	if a.backend == nil {
		return nil
	}

	return a.backend.Close()
}
