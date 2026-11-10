package app_test

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
)

func TestApplicationReadsSavedHistoryDoesNotRefreshAndUsesDesktopVersion(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "history")

	var calls atomic.Int32

	offline := &http.Client{Transport: releaseRoundTrip(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("offline fixture") })}

	reader, err := app.NewApplicationReads(app.ApplicationReadConfig{Inventory: app.ApplicationInventoryConfig{DesktopVersion: "v1.1.0"}, ReleaseClient: offline, HistoryDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}

	if calls.Load() != 0 {
		t.Fatal("constructor did IO")
	}

	if got, err := reader.History(t.Context(), false, false); err != nil || got != nil {
		t.Fatalf("first use=%+v %v", got, err)
	}

	fresh := saveApplicationHistory(t, directory)
	assertSavedChannels(t, reader, fresh.CheckedAt)

	if calls.Load() != 0 {
		t.Fatal("saved read contacted feed")
	}

	got, err := reader.History(t.Context(), false, true)
	if err != nil || got == nil || !got.FromCache || got.RefreshError == "" || calls.Load() != 1 {
		t.Fatalf("explicit fallback=%+v %v calls=%d", got, err, calls.Load())
	}
}

func assertSavedChannels(t *testing.T, reader *app.ApplicationReads, checked time.Time) {
	t.Helper()

	for _, pre := range []bool{false, true} {
		got, err := reader.History(t.Context(), pre, false)
		if err != nil || got == nil || !got.FromCache || got.RefreshError != "" || !got.CheckedAt.Equal(checked) {
			t.Fatalf("saved=%+v %v", got, err)
		}

		want := "v1.3.0"
		if pre {
			want = "v2.0.0-beta.1"
		}

		if got.Available == nil || got.Available.Tag != want {
			t.Fatalf("channel selection=%+v", got)
		}
	}
}
