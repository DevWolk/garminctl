//go:build desktop

package desktop

import (
	"context"
	"errors"
	"net/url"
	"reflect"
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

func TestWindowCloseWaitsForReadyAndFrontendConsent(t *testing.T) {
	t.Parallel()

	host := new(fakeWindowHost)
	app := &App{host: host}
	app.startup(t.Context())
	t.Cleanup(app.downloads.close)

	if app.beforeClose(t.Context()) || len(host.actions) != 0 {
		t.Fatal("unready frontend vetoed window closure")
	}

	app.Ready()

	if !app.beforeClose(t.Context()) || !reflect.DeepEqual(host.actions, []string{"close"}) || host.quits != 0 {
		t.Fatal("ready window did not ask frontend before closing")
	}

	host.quit = func() {
		if _, err := app.downloads.windowContext(); !errors.Is(err, context.Canceled) {
			t.Errorf("quit happened before download cancellation: %v", err)
		}
	}

	app.FinishClose()

	if host.quits != 1 || app.beforeClose(t.Context()) {
		t.Fatal("confirmed close was vetoed or did not quit")
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
	t.Cleanup(app.downloads.close)

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
