package ciqstore

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func schemaCacheTest(t *testing.T) (*bytes.Buffer, *int) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())

	page, err := os.ReadFile("testdata/easyplus-v24-vivoactive5.html")
	if err != nil {
		t.Fatal(err)
	}

	calls := new(int)

	replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		*calls++
		return storeResponse(req, http.StatusOK, string(page)), nil
	})

	logs := new(bytes.Buffer)
	previous := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return logs, calls
}

func TestSchemaCacheFailureWarnsAndRetainsFetchedSettings(t *testing.T) { //nolint:paralleltest // uses process-wide cache/client/logger settings
	logs, calls := schemaCacheTest(t)

	parent := filepath.Dir(CacheDir())
	if err := os.WriteFile(parent, []byte("blocked directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	schema, err := Load(t.Context(), testAppID, 12, testPartNumber)
	if err != nil || schema == nil || len(schema.Settings) != 88 {
		t.Fatalf("valid fetched schema discarded: %v, %v", schema, err)
	}

	if *calls != 1 || !strings.Contains(logs.String(), "settings schema cache write failed") || !strings.Contains(logs.String(), "error=") {
		t.Fatalf("cache failure was silent: calls=%d log=%s", *calls, logs)
	}

	if _, ok := schema.Get("bar1a"); !ok {
		t.Fatal("fetched schema lost its index")
	}

	if _, err := Load(t.Context(), testAppID, 12, testPartNumber); err != nil {
		t.Fatal(err)
	}

	if *calls != 2 {
		t.Fatal("failed persistence was treated as a cache hit")
	}
}

func TestSchemaCacheSuccessLoadsWithoutAnotherRequest(t *testing.T) { //nolint:paralleltest // uses process-wide cache/client/logger settings
	logs, calls := schemaCacheTest(t)

	first, err := Load(t.Context(), testAppID, 12, testPartNumber)
	if err != nil {
		t.Fatal(err)
	}

	second, err := Load(t.Context(), testAppID, 12, testPartNumber)
	if err != nil {
		t.Fatal(err)
	}

	if *calls != 1 || logs.Len() != 0 || len(second.Settings) != len(first.Settings) || second.AppID != first.AppID {
		t.Fatalf("successful cache did not round trip: calls=%d, log=%s", *calls, logs)
	}

	if _, ok := second.Get("bar1a"); !ok {
		t.Fatal("cached schema lost its index")
	}

	entries, err := os.ReadDir(CacheDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache temporary files leaked: %v, %v", entries, err)
	}

	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode=%v", info.Mode())
	}
}

func TestSchemaCacheFailedReplacementCleansTemporaryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	target := filepath.Join(dir, "schema.json")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(target, "previous"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := saveSchema(target, &Schema{AppID: testAppID}); err == nil {
		t.Fatal("failed replacement returned success")
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file leaked: %v, %v", entries, err)
	}

	original, err := os.ReadFile(filepath.Join(target, "previous")) //nolint:gosec // fixed test filename beneath t.TempDir
	if err != nil || string(original) != "keep" {
		t.Fatalf("previous cache entry changed: %q, %v", original, err)
	}
}
