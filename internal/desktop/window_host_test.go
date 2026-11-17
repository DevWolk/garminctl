//go:build desktop

package desktop

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type fakeWindowHost struct {
	actions    []string
	quits      int
	external   []string
	names      []string
	dialogPath string
	dialogErr  error
	quit       func()

	replace      bool
	replaceAsked []string
}

func (h *fakeWindowHost) Emit(action string) { h.actions = append(h.actions, action) }
func (h *fakeWindowHost) Quit() {
	h.quits++
	if h.quit != nil {
		h.quit()
	}
}

func (h *fakeWindowHost) OpenExternal(raw string) error {
	h.external = append(h.external, raw)
	return nil
}

func (h *fakeWindowHost) SaveFile(_ context.Context, name string) (string, error) {
	h.names = append(h.names, name)
	return h.dialogPath, h.dialogErr
}

func (h *fakeWindowHost) ConfirmReplace(_ context.Context, path string) (bool, error) {
	h.replaceAsked = append(h.replaceAsked, path)
	return h.replace, nil
}

func TestWindowCloseWaitsForReadyAndFrontendConsent(t *testing.T) {
	t.Parallel()

	host := new(fakeWindowHost)
	app := &App{host: host}
	app.startup(t.Context())
	t.Cleanup(app.operations.close)

	if app.beforeClose(t.Context()) || len(host.actions) != 0 {
		t.Fatal("unready frontend vetoed window closure")
	}

	app.Ready()

	if !app.beforeClose(t.Context()) || !reflect.DeepEqual(host.actions, []string{"close"}) || host.quits != 0 {
		t.Fatal("ready window did not ask frontend before closing")
	}

	host.quit = func() {
		if _, err := app.operations.windowContext(); !errors.Is(err, context.Canceled) {
			t.Errorf("quit happened before download cancellation: %v", err)
		}
	}

	app.FinishClose()

	if host.quits != 1 || app.beforeClose(t.Context()) {
		t.Fatal("confirmed close was vetoed or did not quit")
	}
}

func TestWindowCanCloseAfterNativePageFailureOnReload(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
		}
	}))
	t.Cleanup(backend.Close)

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	host := new(fakeWindowHost)
	app := &App{host: host}
	app.Ready()

	proxy := proxyWithPageFailure(target, nil, func() { app.ready.Store(false) })
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+windowsDesktopHost+"/", nil)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway || app.beforeClose(t.Context()) || len(host.actions) != 0 {
		t.Fatal("failed reload left the close guard waiting for the absent SPA")
	}

	// The reused backend still answers; the page failure did not stop it.
	request.URL.Path = "/api/server"
	response = httptest.NewRecorder()
	proxy.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatal("page failure prevented reuse of the backend")
	}
}

func TestWindowCanCloseAfterReloadFailsOnAnUnreachableBackend(t *testing.T) {
	t.Parallel()

	var listen net.ListenConfig

	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	gone, err := url.Parse("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, path          string
		destination, accept string
		guarded             bool
	}{
		{name: "navigation with fetch metadata", path: "/", destination: "document", accept: "text/html,application/xhtml+xml"},
		{name: "navigation without fetch metadata", path: "/apps/Face", accept: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		{name: "status poll from the loaded page", path: "/api/status", destination: "empty", accept: "*/*", guarded: true},
		{name: "font fetched by the loaded page", path: "/vendor/fonts/inter.woff2", accept: "*/*", guarded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			host := new(fakeWindowHost)
			app := &App{host: host}
			app.Ready()

			proxy := proxyWithPageFailure(gone, nil, func() { app.ready.Store(false) })
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+windowsDesktopHost+test.path, nil)
			request.Header.Set("Accept", test.accept)

			if test.destination != "" {
				request.Header.Set("Sec-Fetch-Dest", test.destination)
			}

			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request)

			if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "<script") {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}

			// A page that still runs answers the close request; a replaced one cannot.
			if guarded := app.beforeClose(t.Context()); guarded != test.guarded || (len(host.actions) == 1) != test.guarded {
				t.Fatalf("close deferred to the page=%v actions=%v, want %v", guarded, host.actions, test.guarded)
			}
		})
	}
}

func TestWindowCloseWaitsForConfirmedWorkThatHoldsTheWindow(t *testing.T) {
	t.Parallel()

	host := new(fakeWindowHost)
	app := &App{host: host}
	app.startup(t.Context())
	t.Cleanup(app.operations.close)
	app.Ready()

	scope := serviceScope{app: app}

	ctx, release, err := scope.Acquire()
	if err != nil {
		t.Fatal(err)
	}

	unhold, err := scope.HoldClose()
	if err != nil {
		t.Fatal(err)
	}

	// Close button, Quit and signals arrive here; the page's own request arrives as FinishClose.
	for range 2 {
		if !app.beforeClose(t.Context()) {
			t.Fatal("window closed during held work")
		}
	}

	app.FinishClose()
	app.ready.Store(false)

	if !app.beforeClose(t.Context()) || len(host.actions) != 0 || host.quits != 0 || ctx.Err() != nil {
		t.Fatalf("held work interrupted: actions=%v quits=%d ctx=%v", host.actions, host.quits, ctx.Err())
	}

	host.quit = func() {
		if !errors.Is(ctx.Err(), context.Canceled) || app.beforeClose(t.Context()) {
			t.Error("postponed close did not cancel the window or was vetoed again")
		}
	}

	release()
	unhold()
	unhold()

	if host.quits != 1 {
		t.Fatalf("postponed close ran %d times", host.quits)
	}

	if _, err := scope.HoldClose(); err == nil {
		t.Fatal("closing window accepted new work")
	}
}

func TestWindowStaysOpenAfterHeldWorkWhenNoCloseWasRequested(t *testing.T) {
	t.Parallel()

	host := new(fakeWindowHost)
	app := &App{host: host}
	app.startup(t.Context())
	t.Cleanup(app.operations.close)
	app.Ready()

	unhold, err := serviceScope{app: app}.HoldClose()
	if err != nil {
		t.Fatal(err)
	}

	unhold()

	if host.quits != 0 || !app.beforeClose(t.Context()) || !reflect.DeepEqual(host.actions, []string{"close"}) {
		t.Fatalf("quits=%d actions=%v", host.quits, host.actions)
	}
}

func TestWindowUpdateExitEndsHeldWorkWithoutASecondQuit(t *testing.T) {
	t.Parallel()

	host := new(fakeWindowHost)
	app := &App{host: host}
	app.startup(t.Context())
	t.Cleanup(app.operations.close)
	app.Ready()

	scope := serviceScope{app: app}

	unhold, err := scope.HoldClose()
	if err != nil {
		t.Fatal(err)
	}

	if !app.beforeClose(t.Context()) {
		t.Fatal("window closed during held work")
	}

	scope.ExitForUpdate()
	unhold()

	if host.quits != 1 || app.beforeClose(t.Context()) {
		t.Fatalf("quits=%d", host.quits)
	}
}

func TestWindowExternalLinksAreValidatedBeforeHostCall(t *testing.T) {
	t.Parallel()

	host := new(fakeWindowHost)

	app := &App{host: host}
	for _, raw := range []string{"file:///tmp/file", "https://user:password@example.com", "javascript:alert(1)"} {
		if err := app.OpenExternal(raw); err == nil {
			t.Fatalf("untrusted link accepted: %q", raw)
		}
	}

	if len(host.external) != 0 {
		t.Fatal("invalid link reached native browser")
	}

	if err := app.OpenExternal("https://example.invalid/releases"); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(host.external, []string{"https://example.invalid/releases"}) {
		t.Fatal("valid external link changed")
	}
}

func TestWindowDownloadDialogKeepsExportBoundaryAndCancellation(t *testing.T) {
	t.Parallel()

	host := new(fakeWindowHost)
	app := &App{host: host, backend: &Backend{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:8787"}}}
	app.startup(t.Context())
	t.Cleanup(app.operations.close)

	if err := app.Download("https://example.invalid/api/file", "file.fit"); err == nil || len(host.names) != 0 {
		t.Fatal("remote download reached Save dialog")
	}

	if err := app.Download("/api/file?path=GARMIN/test.fit", "folder/export.fit"); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(host.names, []string{"export.fit"}) {
		t.Fatalf("Save dialog names=%v", host.names)
	}

	host.dialogErr = errors.New("synthetic dialog failure")
	if err := app.Download("/api/decode?path=GARMIN/test.fit", "export.json"); !errors.Is(err, host.dialogErr) {
		t.Fatalf("dialog error=%v", err)
	}
}
