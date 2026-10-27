//go:build desktop

package desktop

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed desktop.js
var desktopScript []byte

//go:embed icon.png
var appIcon []byte

// App is the small native bridge: lifecycle, browser links and file dialogs.
// Watch operations remain in the shared local API and MCP server.
type App struct {
	ctx       context.Context //nolint:containedctx // Wails supplies a window-lifetime context once; its native runtime methods require it.
	backend   *Backend
	ready     atomic.Bool
	closing   atomic.Bool
	downloads nativeDownloads
}

// Run opens the single native window using an already-started backend.
func Run(backend *Backend) error {
	app := &App{backend: backend}

	dataDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	appMenu := menu.NewMenu()
	view := appMenu.AddSubmenu("View")
	view.AddText("Back", keys.OptionOrAlt("Left"), func(_ *menu.CallbackData) { app.emit("back") })
	view.AddText("Reload", keys.Key("F5"), func(_ *menu.CallbackData) { app.emit("reload") })
	view.AddText("AI & MCP", nil, func(_ *menu.CallbackData) { app.emit("mcp") })
	view.AddSeparator()
	view.AddText("Quit", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) { wailsruntime.Quit(app.ctx) })

	help := appMenu.AddSubmenu("Help")
	help.AddText("Download updates…", nil, func(_ *menu.CallbackData) {
		wailsruntime.BrowserOpenURL(app.ctx, "https://github.com/DevWolk/garminctl/releases/latest")
	})

	return wails.Run(&options.App{
		Title: "garminctl", Width: 1280, Height: 850, MinWidth: 720, MinHeight: 540,
		AssetServer:   &assetserver.Options{Handler: Proxy(backend.URL, desktopScript)},
		Menu:          appMenu,
		OnStartup:     app.startup,
		OnShutdown:    func(context.Context) { app.downloads.close() },
		OnBeforeClose: app.beforeClose,
		Bind:          []any{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "b89eb2b5-b293-455c-b89d-f903a72ae681",
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				wailsruntime.WindowUnminimise(app.ctx)
				wailsruntime.WindowShow(app.ctx)
			},
		},
		Windows: &windows.Options{WebviewUserDataPath: filepath.Join(dataDir, "garminctl", "desktop"), Theme: windows.SystemDefault},
		Linux:   &linux.Options{ProgramName: "garminctl", Icon: appIcon},
	})
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.downloads.startup(ctx)
}

func (a *App) emit(action string) { wailsruntime.EventsEmit(a.ctx, "desktop:action", action) }

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
}

// FinishClose is called after the frontend confirms pending edits may be lost.
func (a *App) FinishClose() {
	a.closing.Store(true)
	a.downloads.cancel()
	wailsruntime.Quit(a.ctx)
}

// OpenExternal opens an ordinary link in the user's default web browser.
func (a *App) OpenExternal(raw string) error {
	u, err := ExternalURL(raw)
	if err != nil {
		return err
	}

	wailsruntime.BrowserOpenURL(a.ctx, u)

	return nil
}

// Download lets the user choose where a watch file or decoded JSON is saved.
func (a *App) Download(relative, name string) error {
	ctx, err := a.downloads.windowContext()
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

	path, err := wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{DefaultFilename: filepath.Base(name), Title: "Save file"})
	if err != nil || path == "" {
		return err
	}

	return a.downloads.save(u, path)
}
