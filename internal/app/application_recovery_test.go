package app

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/paths"
)

func TestApplicationRecoveryExportsOfflineWithoutNativeInventory(t *testing.T) {
	t.Parallel()
	provider, target := downloadPipeline(t, "v1.2.0", false)

	staged, err := provider.Download(t.Context(), application.Release{Tag: "v1.2.0"}, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	if staged == nil {
		t.Fatal("fixture did not retain a package")
	}

	cache, err := (applicationPackageStore{key: provider.key, stageDirectory: provider.stageDirectory, packageDirectory: provider.packageDirectory}).cache(target)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := cache.List(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("retained=%+v error=%v", rows, err)
	}

	port := applicationRecoveryPackages{cache: cache, directory: filepath.Join(t.TempDir(), "recovery")}
	action := application.NewRecoveryAction(port, runtime.GOOS, runtime.GOARCH)

	result, err := action.Export(t.Context(), rows[0].ID)
	if err != nil || result == nil {
		t.Fatalf("export=%+v error=%v", result, err)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(data, downloadTestBytes) {
		t.Fatalf("copied payload=%q error=%v", data, err)
	}

	if result.Record.ID != rows[0].ID || result.Record.Tag != "v1.2.0" {
		t.Fatalf("receipt=%+v", result)
	}

	if entries, err := os.ReadDir(provider.stageDirectory); err != nil || len(entries) != 0 {
		t.Fatalf("worker cleanup=%v error=%v", entries, err)
	}
}

func TestApplicationRecoveryWithoutAKeyListsAndRemovesButRefusesExport(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)

	provider, target := downloadPipeline(t, applicationDownloadTag, false)
	provider.packageDirectory = filepath.Join(paths.Cache(), "application-packages")

	saved, err := provider.Download(t.Context(), application.Release{Tag: applicationDownloadTag}, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"", "invalid", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))} {
		action, err := NewApplicationRecovery(applicationDownloadTag, key)
		if err != nil {
			t.Fatal(err)
		}

		rows, err := action.List(t.Context())
		if err != nil || len(rows) != 1 || rows[0].ID != saved.CacheID {
			t.Fatalf("key %q: metadata=%+v error=%v", key, rows, err)
		}

		if value, err := action.Export(t.Context(), saved.CacheID); value != nil || err == nil || !strings.Contains(err.Error(), "public key") {
			t.Fatalf("key %q: export=%+v error=%v", key, value, err)
		}
	}

	action, err := NewApplicationRecovery(applicationDownloadTag, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := action.Remove(t.Context(), saved.CacheID); err != nil {
		t.Fatal(err)
	}

	if rows, err := action.List(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("after removal: metadata=%+v error=%v", rows, err)
	}
}
