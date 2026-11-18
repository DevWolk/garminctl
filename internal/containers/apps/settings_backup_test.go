package apps

import (
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/paths"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestSetAppSettingsBacksUpSettingsFile(t *testing.T) { //nolint:paralleltest // replaces the shared schema HTTP client and cache environment
	schemaCacheHTTP(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })

	cache := paths.Cache()
	d, out, _ := registryFixture(t)
	app := connectIQApp(t, out)

	original, err := os.ReadFile(filepath.Join("..", "..", "ship", "ciq", "testdata", testdump.AppRef+".SET"))
	if err != nil {
		t.Fatal(err)
	}

	settingsPath := path.Join(watchsession.PathAppSettings, app.FileName+".SET")
	if err := d.Mkdir(t.Context(), watchsession.PathAppSettings); err != nil {
		t.Fatal(err)
	}

	if err := d.Device.Write(t.Context(), settingsPath, original); err != nil {
		t.Fatal(err)
	}

	snapshots := func() []string {
		t.Helper()

		copies, err := filepath.Glob(filepath.Join(cache, app.FileName+".SET-*", app.FileName+".SET"))
		if err != nil {
			t.Fatal(err)
		}

		return copies
	}

	d.before = func() error {
		if len(snapshots()) != d.writes {
			return errors.New("settings written before publishing a distinct backup")
		}

		return nil
	}

	sess := watchsession.New(d)

	if _, err := NewApplySettingsAction(new(SchemaCache)).Run(t.Context(), sess, app.FileName, map[string]any{}); err != nil {
		t.Fatal(err)
	}

	first := snapshots()
	if len(first) != 1 {
		t.Fatalf("snapshots after one write = %v", first)
	}

	assertRegistryBytes(t, first[0], original)
	assertRegistryBackupProtection(t, first[0])

	// A failed transfer still leaves the copy and names it.
	written, err := d.Read(t.Context(), settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	d.fail = errors.New("synthetic device write failure")

	_, err = NewApplySettingsAction(new(SchemaCache)).Run(t.Context(), sess, app.FileName, map[string]any{})
	if !errors.Is(err, d.fail) || !strings.Contains(err.Error(), cache) {
		t.Fatalf("write failure does not name the backup: %v", err)
	}

	second := snapshots()
	if len(second) != 2 {
		t.Fatalf("snapshots after a failed write = %v", second)
	}

	for _, snapshot := range second {
		if snapshot != first[0] {
			assertRegistryBytes(t, snapshot, written)
		}
	}
}

// connectIQApp returns a registry entry that owns a PRG file, as settings
// lookups require.
func connectIQApp(t *testing.T, out []byte) App {
	t.Helper()

	apps, err := ParseAppsConfig(out)
	if err != nil {
		t.Fatal(err)
	}

	for _, app := range apps {
		if app.FileName != "" {
			return app
		}
	}

	t.Fatal("fixture has no Connect IQ app")

	return App{}
}

func TestSetAppSettingsReadbackReusesLoadedSchema(t *testing.T) { //nolint:paralleltest // replaces the shared schema HTTP client and cache environment
	var requests atomic.Int32

	schemaCacheHTTP(t, func(req *http.Request) (*http.Response, error) {
		requests.Add(1)

		if !strings.Contains(req.URL.Path, "/appSettings2/") {
			return syncResponse(req, []byte(`{"latestInternalVersion":1}`)), nil
		}

		return schemaFormResponse(req, "value"), nil
	})

	d, out, _ := registryFixture(t)
	app := connectIQApp(t, out)

	original, err := os.ReadFile(filepath.Join("..", "..", "ship", "ciq", "testdata", testdump.AppRef+".SET"))
	if err != nil {
		t.Fatal(err)
	}

	if err := d.Mkdir(t.Context(), watchsession.PathAppSettings); err != nil {
		t.Fatal(err)
	}

	if err := d.Device.Write(t.Context(), path.Join(watchsession.PathAppSettings, app.FileName+".SET"), original); err != nil {
		t.Fatal(err)
	}

	var beforeWrite int32

	d.before = func() error { beforeWrite = requests.Load(); return nil }

	as, err := NewApplySettingsAction(new(SchemaCache)).Run(t.Context(), watchsession.New(d), app.FileName, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	if as.SchemaError != "" || beforeWrite == 0 {
		t.Fatalf("schema not loaded before the write: error=%q requests=%d", as.SchemaError, beforeWrite)
	}

	if after := requests.Load(); after != beforeWrite {
		t.Fatalf("readback fetched the schema again under the USB lease: %d requests before the write, %d after", beforeWrite, after)
	}
}
