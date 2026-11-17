package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/paths"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

func writeApplicationState(t *testing.T, base, name string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(base, filepath.Dir(name)), 0o700); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readApplicationState(t *testing.T, base, name string) []byte {
	t.Helper()

	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestApplicationSnapshotPreservesSelectedOpaqueState(t *testing.T) { //nolint:paralleltest // Uses isolated process account/cache paths with a production backend.
	f := applicationPreflightFixture(t)
	config, cache := filepath.Dir(gc.DefaultStorePath()), paths.Cache()

	selected := map[string][]byte{
		"config/garmin-connect.json":                             []byte(`{"future":null,"token":"fake-account"}`),
		"config/garmin-connect.json.mfa":                         []byte(`{"version":99,"cookie":"fake-pending"}`),
		"cache/uploaded-123.json":                                []byte(`{"file.fit":123}`),
		"cache/OUT.BIN-20261112-090001-123456/OUT.BIN":           {0, 1, 255},
		"cache/OUT.BIN-20261112-090001-123456/IN.BIN":            {2, 0, 1},
		"cache/FACE0001.SET-20261112-090001-123456/FACE0001.SET": {255, 2, 0},
	}
	for name, data := range selected {
		area, relative, _ := strings.Cut(name, "/")

		base := config
		if area == "cache" {
			base = cache
		}

		writeApplicationState(t, base, relative, data)
	}

	writeApplicationState(t, config, "desktop/profile", []byte("live profile stays in place"))
	writeApplicationState(t, cache, "activities-123.json", []byte("rebuildable"))

	prepared, err := f.reader.Preflight(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := prepared.Close(); err != nil {
			t.Error(err)
		}
	}()

	receipt, err := f.reader.BackupData(t.Context(), prepared)
	if err != nil || receipt == nil || receipt.Files != len(selected) {
		t.Fatalf("snapshot=%+v %v", receipt, err)
	}

	assertApplicationSnapshotFiles(t, receipt, selected)

	if got := readApplicationState(t, config, "desktop/profile"); string(got) != "live profile stays in place" {
		t.Fatal("live browser profile changed")
	}

	if f.backend.runtime.Manager().Gate().Snapshot().State != lifecycle.Maintenance {
		t.Fatal("snapshot returned install admission too early")
	}

	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}

	f.assertClean(t)
}

func assertApplicationSnapshotFiles(t *testing.T, receipt *application.DataSnapshot, selected map[string][]byte) {
	t.Helper()

	for name, want := range selected {
		if got := readApplicationState(t, receipt.Directory, filepath.FromSlash(name)); !bytes.Equal(got, want) {
			t.Fatalf("%s changed", name)
		}
	}

	manifest := readApplicationState(t, receipt.Directory, "manifest.json")
	if bytes.Contains(manifest, []byte("fake-account")) || bytes.Contains(manifest, []byte("fake-pending")) {
		t.Fatal("manifest contains credentials")
	}

	for _, name := range []string{"config/desktop", "cache/activities-123.json", "cache/application-packages"} {
		if _, err := os.Stat(filepath.Join(receipt.Directory, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected copied state: %s %v", name, err)
		}
	}
}

func TestApplicationSnapshotFailureResumesSameBackend(t *testing.T) { //nolint:paralleltest // Uses isolated process account/cache paths.
	f := applicationPreflightFixture(t)
	config := filepath.Dir(gc.DefaultStorePath())
	writeApplicationState(t, config, "garmin-connect.json", []byte("opaque fake state"))
	writeApplicationState(t, config, "application-backups", []byte("blocked destination"))

	prepared, err := f.reader.Preflight(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	if result, err := f.reader.BackupData(t.Context(), prepared); result != nil || err == nil {
		t.Fatalf("blocked destination accepted: %v %v", result, err)
	}

	if f.backend.runtime.Manager().Gate().Snapshot().State != lifecycle.Running {
		t.Fatal("backup error stranded maintenance")
	}

	if got := readApplicationState(t, config, "garmin-connect.json"); string(got) != "opaque fake state" {
		t.Fatal("source changed on failure")
	}

	f.assertClean(t)
}

func TestApplicationSnapshotContextUsesOnlyOwnedLiveMaintenance(t *testing.T) { //nolint:paralleltest // Production fixture isolates the account path.
	f := serveApplicationBackend(t)
	if _, _, err := f.control.snapshotContextFor(f.address, true)(t.Context()); err == nil {
		t.Fatal("snapshot accepted without maintenance")
	}

	release, err := f.control.maintenanceFor(f.address, true)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	//nolint:paralleltest // Shared owned-runtime ticket is deliberately reused in order.
	for _, test := range []struct {
		name, address string
		owned         bool
	}{
		{name: "reused", address: f.address}, {name: "wrong backend", address: "http://127.0.0.1:1", owned: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := f.control.snapshotContextFor(test.address, test.owned)(t.Context()); err == nil {
				t.Fatal("foreign backend accepted")
			}
		})
	}

	bound, cancel, err := f.control.snapshotContextFor(f.address, true)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	f.cancel()
	awaitApplicationBarrier(t, bound.Done())
	awaitApplicationBarrier(t, f.done)

	if _, _, err := f.control.snapshotContextFor(f.address, true)(t.Context()); !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("stopped=%v", err)
	}
}
