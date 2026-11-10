//go:build desktop

package desktop

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
func Run(start func() (*Backend, error), registrars ...ServiceRegistrar) (runErr error) {
	dataDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	service := new(App)
	assets := new(nativeAssets)
	host := newV3WindowHost(service, dataDir, assets, nativeServices(service, registrars))

	service.host = host
	defer func() { runErr = errors.Join(runErr, service.shutdown()) }()

	service.backend, err = start()

	startupErr := err
	if startupErr != nil {
		assets.set(startupErrorHandler(startupErr))
	} else {
		assets.set(proxyWithPageFailure(service.backend.URL, desktopScript, func() { service.ready.Store(false) }))
	}

	window := host.app.Window.NewWithOptions(windowOptions(startupErr))
	host.window.Store(window)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if service.beforeClose(host.app.Context()) {
			event.Cancel()
		}
	})

	service.startup(host.app.Context())

	return errors.Join(startupErr, host.app.Run())
}

func newV3WindowHost(service *App, dataDir string, assets http.Handler, services []application.Service) *v3WindowHost {
	host := new(v3WindowHost)
	host.app = application.New(application.Options{
		Name: applicationName, Icon: appIcon,
		Assets:     nativeAssetOptions(assets),
		Services:   services,
		ShouldQuit: func() bool { return !service.beforeClose(context.Background()) },
		// The production logger discards output, so a fatal host error (for
		// example no session bus for the single-instance check) is reported here.
		ErrorHandler: func(err error) { slog.Error("desktop host error", "error", err) },
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

func nativeAssetOptions(handler http.Handler) application.AssetOptions {
	return application.AssetOptions{Handler: handler, Middleware: guardNativeAssets}
}

// Wails dispatches runtime calls before the asset handler, so this guard must
// wrap the entire native asset pipeline rather than only the backend proxy.
func guardNativeAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !desktopOrigin(r) {
			http.Error(w, "forbidden: not a desktop request", http.StatusForbidden)
			return
		}

		if r.URL.Path == "/wails/runtime" && r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		next.ServeHTTP(w, r)
	})
}

// windowOptions describes the main window, or the smaller startup error window.
// Windows shows the application menu only when a window opts in.
func windowOptions(startupErr error) application.WebviewWindowOptions {
	options := application.WebviewWindowOptions{Title: applicationName, Width: 1280, Height: 850, MinWidth: 720, MinHeight: 540, URL: "/", UseApplicationMenu: true}
	if startupErr != nil {
		options.Title = "garminctl — startup error"
		options.Width = 640
		options.Height = 360
		options.MinWidth = 0
		options.MinHeight = 0
	}

	return options
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
	help.Add("Application updates…").OnClick(func(_ *application.Context) { service.emit("application") })
	h.app.Menu.Set(menu)
}
func (h *v3WindowHost) Emit(action string)            { h.app.Event.Emit("desktop:action", action) }
func (h *v3WindowHost) Quit()                         { h.app.Quit() }
func (h *v3WindowHost) OpenExternal(raw string) error { return h.app.Browser.OpenURL(raw) }
func (h *v3WindowHost) SaveFile(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	path, err := h.app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{Title: "Save file", Filename: name, Window: h.window.Load(), CanCreateDirectories: true}).PromptForSingleSelection()

	return nativeSaveFileResult(runtime.GOOS, path, err)
}

func nativeSaveFileResult(platform, path string, err error) (string, error) {
	// Wails returns an internal CFD sentinel on Windows Cancel. It is not
	// exported, so normalize only its exact message with an empty selection.
	if platform == "windows" && path == "" && err != nil && err.Error() == "cancelled by user" {
		return "", nil
	}

	return path, err
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

// ConfirmReplace asks on Linux, where the GTK Save dialog does not; the
// Windows Save dialog already confirmed the replacement.
func (h *v3WindowHost) ConfirmReplace(ctx context.Context, target string) (bool, error) {
	if runtime.GOOS != "linux" {
		return true, nil
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	answer := make(chan bool, 1)

	dialog := h.app.Dialog.Question().SetTitle("Replace file?").SetMessage(filepath.Base(target) + " already exists. Replace it?")
	if window := h.window.Load(); window != nil {
		dialog.AttachToWindow(window)
	}

	dialog.AddButton("Cancel").SetAsCancel().OnClick(func() { answer <- false })
	dialog.AddButton("Replace").SetAsDefault().OnClick(func() { answer <- true })
	dialog.Show()

	select {
	case replace := <-answer:
		return replace, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
