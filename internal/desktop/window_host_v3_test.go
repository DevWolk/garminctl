//go:build desktop

package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNativeSaveFileResult(t *testing.T) {
	t.Parallel()

	const windowsPlatform = "windows"

	cancelled := errors.New("cancelled by user")
	failure := errors.New("dialog initialization failed")

	for _, test := range []struct {
		name     string
		platform string
		path     string
		err      error
		wantErr  error
	}{
		{name: "Windows user cancel", platform: windowsPlatform, err: cancelled},
		{name: "Linux user cancel", platform: "linux"},
		{name: "Windows selected file", platform: windowsPlatform, path: "export.fit"},
		{name: "Windows system failure", platform: windowsPlatform, err: failure, wantErr: failure},
		{name: "context cancellation", platform: windowsPlatform, err: context.Canceled, wantErr: context.Canceled},
		{name: "not Windows", platform: "linux", err: cancelled, wantErr: cancelled},
		{name: "nonempty path", platform: windowsPlatform, path: "export.fit", err: cancelled, wantErr: cancelled},
		{name: "wrapped failure", platform: windowsPlatform, err: fmt.Errorf("initialize: %w", cancelled), wantErr: cancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path, err := nativeSaveFileResult(test.platform, test.path, test.err)
			if path != test.path || !errors.Is(err, test.wantErr) {
				t.Fatalf("selection=(%q, %v), want=(%q, %v)", path, err, test.path, test.wantErr)
			}
		})
	}
}

func TestNativeServiceBridgeContract(t *testing.T) {
	t.Parallel()

	service := reflect.TypeFor[*App]()

	prefix := service.Elem().PkgPath() + "." + service.Elem().Name() + "."
	if !strings.Contains(string(desktopScript), "Call.ByName('"+prefix+"'") {
		t.Fatal("frontend does not use the service's complete native method path")
	}

	for _, contract := range []struct {
		name    string
		in, out []reflect.Type
	}{
		{name: "Ready"},
		{name: "FinishClose"},
		{name: "OpenExternal", in: []reflect.Type{reflect.TypeFor[string]()}, out: []reflect.Type{reflect.TypeFor[error]()}},
		{name: "Download", in: []reflect.Type{reflect.TypeFor[string](), reflect.TypeFor[string]()}, out: []reflect.Type{reflect.TypeFor[error]()}},
	} {
		method, ok := service.MethodByName(contract.name)
		if !ok {
			t.Fatalf("missing native method %s", contract.name)
		}

		if method.Type.NumIn() != len(contract.in)+1 || method.Type.NumOut() != len(contract.out) {
			t.Fatalf("native method signature changed: %s", contract.name)
		}

		for i, in := range contract.in {
			if method.Type.In(i+1) != in {
				t.Fatalf("%s argument %d", contract.name, i)
			}
		}

		for i, out := range contract.out {
			if method.Type.Out(i) != out {
				t.Fatalf("%s result %d", contract.name, i)
			}
		}
	}
}

func TestNativeShutdownJoinsOwnedBackendOnce(t *testing.T) {
	t.Parallel()

	var stops atomic.Int32

	done := make(chan error, 1)
	failure := errors.New("synthetic backend stop failure")
	app := &App{backend: &Backend{cancel: func() { stops.Add(1); done <- failure }, done: done}}
	app.startup(t.Context())

	for range 2 {
		if err := app.shutdown(); !errors.Is(err, failure) {
			t.Fatalf("shutdown lost backend result: %v", err)
		}
	}

	if stops.Load() != 1 {
		t.Fatalf("native shutdown stopped backend %d times", stops.Load())
	}

	if _, err := app.operations.windowContext(); !errors.Is(err, context.Canceled) {
		t.Fatalf("downloads survived native shutdown: %v", err)
	}

	if err := (&App{backend: &Backend{}}).shutdown(); err != nil {
		t.Fatalf("reused backend shutdown=%v", err)
	}
}

func TestNativeStartupFailurePageEscapesError(t *testing.T) {
	t.Parallel()

	assets := new(nativeAssets)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://wails.localhost/", nil)
	before := httptest.NewRecorder()
	assets.ServeHTTP(before, req)

	if before.Code != http.StatusNotFound {
		t.Fatal("uninitialized native host exposed assets")
	}

	assets.set(startupErrorHandler(errors.New("<script>alert('failure')</script>")))

	response := httptest.NewRecorder()
	assets.ServeHTTP(response, req)

	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), "&lt;script&gt;") {
		t.Fatal("startup error was not safely rendered")
	}
}

func TestWindowOptionsUseApplicationMenu(t *testing.T) {
	t.Parallel()

	// Wails v3 attaches the application menu to a Windows window only on
	// request; Linux falls back to it either way.
	for _, startupErr := range []error{nil, errors.New("backend failed")} {
		options := windowOptions(startupErr)
		if !options.UseApplicationMenu {
			t.Fatalf("startup error %v: window does not use the application menu", startupErr)
		}
	}

	if options := windowOptions(nil); options.Width != 1280 || options.Height != 850 || options.URL != "/" {
		t.Fatalf("main window options = %+v", options)
	}

	if options := windowOptions(errors.New("backend failed")); options.Width != 640 || options.MinWidth != 0 {
		t.Fatalf("startup error window options = %+v", options)
	}
}
