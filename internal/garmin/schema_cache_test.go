package garmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/device"
)

const (
	schemaTestID     = "schema-app"
	schemaInvalidate = "invalidate"
	schemaReconnect  = "reconnect"
	schemaRefresh    = "refresh"
)

func schemaXML(part string) string {
	return `<Device><Id>42</Id><Model><PartNumber>` + part + `</PartNumber></Model></Device>`
}

func schemaCacheHTTP(t *testing.T, fetch syncTransport) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LOCALAPPDATA", root)

	before := ciqstore.HTTPClient
	ciqstore.HTTPClient = &http.Client{Transport: fetch}

	t.Cleanup(func() { ciqstore.HTTPClient = before })
}

func schemaFormResponse(req *http.Request, value string) *http.Response {
	return syncResponse(req, []byte(`<input type="text" name="field" value="`+value+`">`))
}

func schemaApp() *App { return &App{ID: schemaTestID, FileName: "schema.PRG", Version: 1} }

func requireAppSchema(t *testing.T, svc *Service, app *App) *ciqstore.Schema {
	t.Helper()

	sc, err := svc.appSchema(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}

	if len(sc.Settings) != 1 {
		t.Fatalf("schema settings=%+v", sc.Settings)
	}

	return sc
}

func TestAppSchemaCacheTracksVersionAndDevice(t *testing.T) { //nolint:paralleltest // replaces the shared schema HTTP client and cache environment
	latest := 0

	schemaCacheHTTP(t, func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/appSettings2/") {
			return schemaFormResponse(req, "identity"), nil
		}

		latest++

		return syncResponse(req, []byte(`{"latestInternalVersion":2}`)), nil
	})

	d := &cacheTestDevice{data: []byte(schemaXML("PART-A"))}
	svc := New(d)
	app := schemaApp()

	first := requireAppSchema(t, svc, app)
	if first.Version != 1 || first.PartNumber != "PART-A" {
		t.Fatalf("first schema=%+v", first)
	}

	app.Version = 2
	if second := requireAppSchema(t, svc, app); second.Version != 2 {
		t.Fatalf("changed app version returned schema version %d", second.Version)
	}

	d.replace(schemaXML("PART-B"), true)

	if other := requireAppSchema(t, svc, app); other.PartNumber != "PART-B" || other.Version != 2 {
		t.Fatalf("changed device returned schema=%+v", other)
	}

	beforeUnitChange := latest

	d.replace(strings.Replace(schemaXML("PART-B"), "<Id>42</Id>", "<Id>43</Id>", 1), false)

	if _, err := svc.read(t.Context(), PathDeviceXML, false); err != nil {
		t.Fatal(err)
	}

	requireAppSchema(t, svc, app)

	if latest != beforeUnitChange+1 {
		t.Fatalf("changed unit reused previous device's schema; metadata calls=%d", latest)
	}
}

func TestAppSchemaCacheInvalidationReloadsDiskEntry(t *testing.T) { //nolint:paralleltest // replaces the shared schema HTTP client and cache environment
	for _, boundary := range []string{schemaRefresh, schemaInvalidate, schemaReconnect} { //nolint:paralleltest // cases replace the shared HTTP client and run sequentially
		t.Run(boundary, func(t *testing.T) {
			schemaCacheHTTP(t, func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "/appSettings2/") {
					return schemaFormResponse(req, "before-boundary"), nil
				}

				return syncResponse(req, []byte(`{"latestInternalVersion":1}`)), nil
			})

			d := &cacheTestDevice{data: []byte(schemaXML("PART-A"))}
			svc := New(d)
			app := schemaApp()
			previous := requireAppSchema(t, svc, app)
			updated := *previous
			updated.Settings = []ciqstore.Setting{{Key: "field", Type: "text", Default: "after-boundary"}}

			data, err := json.Marshal(&updated)
			if err != nil {
				t.Fatal(err)
			}

			cache := filepath.Join(ciqstore.CacheDir(), schemaTestID+"-v1-PART-A.json")
			if err := os.WriteFile(cache, data, 0o600); err != nil {
				t.Fatal(err)
			}

			schemaBoundary(svc, d, boundary)

			if got := requireAppSchema(t, svc, app); got.Settings[0].Default != "after-boundary" {
				t.Fatalf("%s retained old in-memory schema: %+v", boundary, got.Settings)
			}
		})
	}
}

func schemaBoundary(svc *Service, d *cacheTestDevice, boundary string) {
	switch boundary {
	case schemaRefresh:
		svc.Refresh()
	case schemaInvalidate:
		svc.invalidate(PathAppsConfig)
	case schemaReconnect:
		d.replace(schemaXML("PART-A"), true)
	}
}

func TestLateSchemaFetchCannotReplaceNewGeneration(t *testing.T) { //nolint:paralleltest // replaces the shared schema HTTP client and cache environment
	for _, boundary := range []string{schemaRefresh, schemaInvalidate, schemaReconnect} { //nolint:paralleltest // cases replace the shared HTTP client and run sequentially
		t.Run(boundary, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})

			var once sync.Once

			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)

			var fetches atomic.Int32

			schemaCacheHTTP(t, func(req *http.Request) (*http.Response, error) {
				if !strings.Contains(req.URL.Path, "/appSettings2/") {
					return syncResponse(req, []byte(`{"latestInternalVersion":1}`)), nil
				}

				if fetches.Add(1) == 1 {
					close(entered)

					select {
					case <-release:
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}

					return schemaFormResponse(req, "old-response"), nil
				}

				return schemaFormResponse(req, "new-response"), nil
			})

			d := &cacheTestDevice{data: []byte(schemaXML("PART-A"))}
			svc := New(d)
			app := schemaApp()
			done := make(chan error, 1)

			go func() {
				sc, err := svc.appSchema(t.Context(), app)
				if err == nil && sc.Settings[0].Default != "old-response" {
					err = fmt.Errorf("unexpected old result: %+v", sc.Settings)
				}

				done <- err
			}()

			<-entered
			schemaBoundary(svc, d, boundary)

			if got := requireAppSchema(t, svc, app); got.Settings[0].Default != "new-response" {
				t.Fatalf("new generation schema=%+v", got.Settings)
			}

			unblock()

			if err := <-done; err != nil {
				t.Fatal(err)
			}

			if got := requireAppSchema(t, svc, app); got.Settings[0].Default != "new-response" {
				t.Fatalf("late fetch replaced newer schema: %+v", got.Settings)
			}
		})
	}
}

func TestSessionMutationInvalidatesFeatureSchemaCache(t *testing.T) { //nolint:paralleltest // replaces the shared schema HTTP client and cache environment
	for _, raw := range []bool{false, true} { //nolint:paralleltest // cases share the schema client and environment
		t.Run(fmt.Sprintf("raw=%v", raw), func(t *testing.T) {
			var metadataCalls int

			schemaCacheHTTP(t, func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "/appSettings2/") {
					return schemaFormResponse(req, "schema"), nil
				}

				metadataCalls++

				return syncResponse(req, []byte(`{"latestInternalVersion":1}`)), nil
			})

			d := &mutationTestDevice{
				cacheTestDevice: &cacheTestDevice{data: []byte(schemaXML("PART-A"))},
				failure:         device.ErrDisconnected,
			}
			svc := New(d)
			requireAppSchema(t, svc, schemaApp())

			var err error
			if raw {
				_, _, _, err = svc.RawMTPRead(t.Context(), 0x9999, nil)
			} else {
				err = svc.WriteFile(t.Context(), "other-file", nil)
			}

			if !errors.Is(err, device.ErrDisconnected) {
				t.Fatalf("mutation error = %v", err)
			}

			// This fake mutates storage before returning a failure. Restore its XML
			// payload, without Refresh, so only the attempted mutation invalidates.
			d.replace(schemaXML("PART-A"), false)
			requireAppSchema(t, svc, schemaApp())

			if metadataCalls != 2 {
				t.Fatalf("mutation retained the old feature cache: metadata calls=%d", metadataCalls)
			}
		})
	}
}

// Keep these tests on the legacy facade while the actual schema owner is apps.
func (s *Service) appSchema(ctx context.Context, app *App) (*ciqstore.Schema, error) {
	return s.schemas.Load(ctx, s.session, app)
}
