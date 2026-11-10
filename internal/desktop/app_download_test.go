//go:build desktop

package desktop

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestAppDownloadBeforeStartupDoesNotOpenDialog(t *testing.T) {
	t.Parallel()

	a := &App{}
	if err := a.Download("/api/file?path=GARMIN/example.fit", "example.fit"); err == nil {
		t.Fatal("download before startup succeeded")
	}
}

func TestAppDownloadAfterWindowCloseDoesNotOpenDialog(t *testing.T) {
	t.Parallel()

	a := &App{}
	a.startup(t.Context())
	a.operations.close()

	if err := a.Download("/api/file?path=GARMIN/example.fit", "example.fit"); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed window download = %v", err)
	}
}

func TestAppDownloadWithoutBackendDoesNotOpenDialog(t *testing.T) {
	t.Parallel()

	a := &App{}
	a.startup(t.Context())
	t.Cleanup(a.operations.close)

	if err := a.Download("/api/file?path=GARMIN/example.fit", "example.fit"); err == nil {
		t.Fatal("download without backend succeeded")
	}
}

func TestAppDownloadAsksBeforeReplacingExistingFile(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)

		_, _ = w.Write([]byte("new"))
	}))
	t.Cleanup(server.Close)

	backend, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	target := filepath.Join(dir, "example.fit")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	host := &fakeWindowHost{dialogPath: target}
	a := &App{host: host, backend: &Backend{URL: backend}}
	a.startup(t.Context())
	t.Cleanup(a.operations.close)

	download := func(want string) {
		t.Helper()

		if err := a.Download("/api/file?path=GARMIN/example.fit", "example.fit"); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(host.dialogPath)
		if err != nil || string(got) != want {
			t.Fatalf("%s holds %q (%v), want %q", host.dialogPath, got, err, want)
		}
	}

	// Declined: the existing file is untouched and nothing is downloaded.
	download("old")

	if requests.Load() != 0 || len(host.replaceAsked) != 1 || host.replaceAsked[0] != target {
		t.Fatalf("declined replacement: requests=%d asked=%v", requests.Load(), host.replaceAsked)
	}

	host.replace = true

	download("new")

	if requests.Load() != 1 || len(host.replaceAsked) != 2 {
		t.Fatalf("accepted replacement: requests=%d asked=%v", requests.Load(), host.replaceAsked)
	}

	// A new file needs no question.
	host.dialogPath = filepath.Join(dir, "fresh.fit")

	download("new")

	if len(host.replaceAsked) != 2 {
		t.Fatalf("new file asked for replacement: %v", host.replaceAsked)
	}
}
