//go:build desktop

package desktop

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type v3WindowHost struct {
	app    *application.App
	window atomic.Pointer[application.WebviewWindow]
}

// Run initializes single-instance ownership before starting an owned backend.
// Native shutdown joins that backend even on framework-specific exit paths.
func Run(start func() (*Backend, error)) (runErr error) {
	dataDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	service := new(App)
	assets := new(nativeAssets)
	host := newV3WindowHost(service, dataDir, assets)

	service.host = host
	defer func() { runErr = errors.Join(runErr, service.shutdown()) }()

	service.backend, err = start()
	startupErr := err
	options := application.WebviewWindowOptions{Title: applicationName, Width: 1280, Height: 850, MinWidth: 720, MinHeight: 540, URL: "/"}

	if startupErr != nil {
		assets.set(startupErrorHandler(startupErr))

		options.Title = "garminctl — startup error"
		options.Width = 640
		options.Height = 360
		options.MinWidth = 0
		options.MinHeight = 0
	} else {
		assets.set(Proxy(service.backend.URL, desktopScript))
	}

	window := host.app.Window.NewWithOptions(options)
	host.window.Store(window)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if service.beforeClose(host.app.Context()) {
			event.Cancel()
		}
	})

	service.startup(host.app.Context())

	return errors.Join(startupErr, host.app.Run())
}

func newV3WindowHost(service *App, dataDir string, assets http.Handler) *v3WindowHost {
	host := new(v3WindowHost)
	host.app = application.New(application.Options{
		Name: applicationName, Icon: appIcon,
		Assets:     application.AssetOptions{Handler: assets},
		Services:   []application.Service{application.NewService(service)},
		ShouldQuit: func() bool { return !service.beforeClose(context.Background()) },
		OnShutdown: func() {
			if err := service.shutdown(); err != nil {
				slog.Error("desktop backend shutdown failed", "error", err)
			}
		},
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "b89eb2b5-b293-455c-b89d-f903a72ae681", OnSecondInstanceLaunch: func(_ application.SecondInstanceData) {
			if window := host.window.Load(); window != nil {
				window.Restore()
				window.Show()
				window.Focus()
			}
		}},
		Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(dataDir, applicationName, "desktop")},
		Linux:   application.LinuxOptions{ProgramName: applicationName},
	})
	host.menu(service)

	return host
}

func (h *v3WindowHost) menu(service *App) {
	menu := h.app.Menu.New()
	view := menu.AddSubmenu("View")
	view.Add("Back").SetAccelerator("OptionOrAlt+Left").OnClick(func(_ *application.Context) { service.emit("back") })
	view.Add("Reload").SetAccelerator("F5").OnClick(func(_ *application.Context) { service.emit("reload") })
	view.Add("AI & MCP").OnClick(func(_ *application.Context) { service.emit("mcp") })
	view.AddSeparator()
	view.Add("Quit").SetAccelerator("CmdOrCtrl+q").OnClick(func(_ *application.Context) { h.Quit() })

	help := menu.AddSubmenu("Help")
	help.Add("Download updates…").OnClick(func(_ *application.Context) {
		_ = h.OpenExternal("https://github.com/DevWolk/garminctl/releases/latest")
	})
	h.app.Menu.Set(menu)
}
func (h *v3WindowHost) Emit(action string)            { h.app.Event.Emit("desktop:action", action) }
func (h *v3WindowHost) Quit()                         { h.app.Quit() }
func (h *v3WindowHost) OpenExternal(raw string) error { return h.app.Browser.OpenURL(raw) }
func (h *v3WindowHost) SaveFile(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	return h.app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{Title: "Save file", Filename: name, Window: h.window.Load(), CanCreateDirectories: true}).PromptForSingleSelection()
}

// nativeAssets publishes immutable handlers once backend startup has resolved.
// A request arriving during native initialization cannot race that publication.
type (
	nativeAssets struct{ page atomic.Pointer[nativePage] }
	nativePage   struct{ handler http.Handler }
)

func (a *nativeAssets) set(handler http.Handler) { a.page.Store(&nativePage{handler: handler}) }
func (a *nativeAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	page := a.page.Load()
	if page == nil {
		http.NotFound(w, r)
		return
	}

	page.handler.ServeHTTP(w, r)
}
