package updatetransport_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func historySnapshot() updatetransport.HistorySnapshot {
	return updatetransport.HistorySnapshot{CheckedAt: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC), Releases: []updatetransport.Release{{Tag: "v1.2.0", Name: "release", Notes: "<plain text>"}, {Tag: "v2.0.0-beta.1", Prerelease: true}, {Tag: "v9.0.0", Draft: true}}}
}

func TestHistoryCacheRoundTrip(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "history")

	cache, err := updatetransport.NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor did IO: %v", err)
	}

	if result, err := cache.Load(t.Context()); result != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %+v %v", result, err)
	}

	want := historySnapshot()
	if err := cache.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}

	fresh, err := updatetransport.NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}

	got, err := fresh.Load(t.Context())
	if err != nil || got == nil || !got.CheckedAt.Equal(want.CheckedAt) || !slices.Equal(got.Releases, want.Releases) {
		t.Fatalf("got=%+v error=%v", got, err)
	}

	got.Releases[0].Tag = "changed"

	again, err := cache.Load(t.Context())
	if err != nil || again.Releases[0].Tag != want.Releases[0].Tag {
		t.Fatalf("cache aliases caller: %+v %v", again, err)
	}
}

func TestHistoryCacheCancellation(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()

	cache, err := updatetransport.NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}

	if err := cache.Save(t.Context(), historySnapshot()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := cache.Save(ctx, updatetransport.HistorySnapshot{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("save canceled: %v", err)
	}

	if result, err := cache.Load(ctx); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("load canceled: %+v %v", result, err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "history.json" {
		t.Fatalf("unexpected files: %v %v", entries, err)
	}
}

func TestHistoryCacheRejectsIncompleteAndForeignSnapshots(t *testing.T) {
	t.Parallel()

	valid := `{"version":1,"source":"https://api.github.com/repos/DevWolk/garminctl/releases","checkedAt":"2026-11-08T12:00:00Z","releases":[]}`
	for _, tc := range []struct{ name, body string }{
		{"partial", valid[:len(valid)-1]},
		{"future schema", strings.Replace(valid, `"version":1`, `"version":2`, 1)},
		{"foreign source", strings.Replace(valid, "DevWolk", "Other", 1)},
		{"zero time", strings.Replace(valid, "2026-11-08T12:00:00Z", "0001-01-01T00:00:00Z", 1)},
		{"missing releases", strings.Replace(valid, `,"releases":[]`, "", 1)},
		{"null releases", strings.Replace(valid, `"releases":[]`, `"releases":null`, 1)},
		{"unknown field", strings.Replace(valid, `"version":1`, `"extra":true,"version":1`, 1)},
		{"extra JSON value", valid + " {}"},
		{"too many records", strings.Replace(valid, `"releases":[]`, `"releases":[`+strings.Repeat(`{},`, 1000)+`{}]`, 1)},
		{"oversized", strings.Repeat(" ", (20<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "history.json"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}

			cache, err := updatetransport.NewHistoryCache(directory)
			if err != nil {
				t.Fatal(err)
			}

			if result, err := cache.Load(t.Context()); err == nil || result != nil {
				t.Fatalf("accepted invalid snapshot: %+v %v", result, err)
			}
		})
	}
}

func TestHistoryCacheAcceptsEmptyHistory(t *testing.T) {
	t.Parallel()

	cache, err := updatetransport.NewHistoryCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := historySnapshot()

	want.Releases = []updatetransport.Release{}
	if err := cache.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}

	got, err := cache.Load(t.Context())
	if err != nil || got == nil || got.Releases == nil || len(got.Releases) != 0 {
		t.Fatalf("empty history=%+v error=%v", got, err)
	}
}

func TestHistoryCacheLimitsAndFailedSaveRetainPrevious(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()

	cache, err := updatetransport.NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}

	want := historySnapshot()
	if err := cache.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []updatetransport.HistorySnapshot{
		{Releases: want.Releases},
		{CheckedAt: want.CheckedAt},
		{CheckedAt: want.CheckedAt, Releases: make([]updatetransport.Release, 1001)},
		{CheckedAt: want.CheckedAt, Releases: []updatetransport.Release{{Notes: strings.Repeat("x", 20<<20)}}},
	} {
		if err := cache.Save(t.Context(), bad); err == nil {
			t.Fatal("accepted invalid/oversized save")
		}

		got, err := cache.Load(t.Context())
		if err != nil || !slices.Equal(got.Releases, want.Releases) {
			t.Fatalf("previous history lost: %+v %v", got, err)
		}
	}

	data := readHistoryTestFile(t, filepath.Join(directory, "history.json"))

	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil || string(wire["version"]) != "1" {
		t.Fatalf("wire=%s error=%v", data, err)
	}
}

func readHistoryTestFile(t *testing.T, path string) []byte {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}

	return data
}
