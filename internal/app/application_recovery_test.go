package app

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
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

func TestApplicationRecoveryRefusesMissingOrRequestSelectedKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "invalid", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))} {
		if value, err := NewApplicationRecovery("v1.2.0", key); value != nil || err == nil {
			t.Fatalf("accepted missing/legacy key=%+v error=%v", value, err)
		}
	}
}
