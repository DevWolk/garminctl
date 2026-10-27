//go:build desktop

package desktop

import (
	"context"
	"errors"
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
	a.downloads.close()

	if err := a.Download("/api/file?path=GARMIN/example.fit", "example.fit"); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed window download = %v", err)
	}
}

func TestAppDownloadWithoutBackendDoesNotOpenDialog(t *testing.T) {
	t.Parallel()

	a := &App{}
	a.startup(t.Context())
	t.Cleanup(a.downloads.close)

	if err := a.Download("/api/file?path=GARMIN/example.fit", "example.fit"); err == nil {
		t.Fatal("download without backend succeeded")
	}
}
