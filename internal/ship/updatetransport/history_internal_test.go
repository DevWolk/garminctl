package updatetransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryReplacementFailurePreservesOldFileAndCleansTemporary(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()

	cache, err := NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}

	want := HistorySnapshot{CheckedAt: time.Now().UTC(), Releases: []Release{{Tag: "v1.2.0"}}}
	if err := cache.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("replace denied")

	failedReplace := func(source, target string) error {
		if filepath.Dir(source) != directory || target != filepath.Join(directory, historyFile) {
			t.Errorf("paths: %s %s", source, target)
		}

		root, err := os.OpenRoot(directory)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()

		if data, err := root.ReadFile(filepath.Base(source)); err != nil || string(data) != "new content" {
			t.Errorf("temporary contents: %s %v", data, err)
		}

		return sentinel
	}
	if err := writeHistoryFile(t.Context(), directory, []byte("new content"), failedReplace); !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}

	got, err := cache.Load(t.Context())
	if err != nil || got.Releases[0].Tag != "v1.2.0" {
		t.Fatalf("old history=%+v error=%v", got, err)
	}

	assertHistoryNoTemporary(t, directory)
}

func TestHistoryCancellationBeforeReplacementCleansTemporary(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	called := false

	err := writeHistoryFile(ctx, directory, []byte("partial"), func(string, string) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error=%v published=%t", err, called)
	}

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial files=%v error=%v", entries, err)
	}
}

func assertHistoryNoTemporary(t *testing.T, directory string) {
	t.Helper()

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != historyFile {
		t.Fatalf("files=%v error=%v", entries, err)
	}
}
