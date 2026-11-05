package apps

import (
	"bytes"
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
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const (
	schemaInvalidate = "invalidate"
	schemaReconnect  = "reconnect"
	schemaRefresh    = "refresh"
)

// schemaWatchDevice serves one replaceable payload for every path and reports
// a reconnect counter that the test can advance.
type schemaWatchDevice struct {
	device.Device
	mu         sync.Mutex
	data       []byte
	reconnects int
}

func (d *schemaWatchDevice) Reconnects() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.reconnects
}

func (d *schemaWatchDevice) Read(context.Context, string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return bytes.Clone(d.data), nil
}

func (d *schemaWatchDevice) replace(data string, reconnect bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.data = []byte(data)
	if reconnect {
		d.reconnects++
	}
}

// failingMutationDevice changes storage and then reports failure, as a
// transport that lost its connection mid-transfer can.
type failingMutationDevice struct {
	*schemaWatchDevice
	failure error
}

func (d *failingMutationDevice) Write(context.Context, string, []byte) error {
	d.replace("changed", false)
	return d.failure
}

func (d *failingMutationDevice) MtpRead(context.Context, uint32, []uint32) ([]byte, uint32, []uint32, error) {
	d.replace("changed", false)
	return nil, 0x2001, nil, d.failure
}

// schemaBoundary crosses one cache boundary the way the connection owner does:
// a Session invalidation is followed by a Reset of the feature cache, and a
// reconnect is observed through the transport counter alone.
func schemaBoundary(cache *SchemaCache, sess *watchsession.Session, d *schemaWatchDevice, boundary string) {
	switch boundary {
	case schemaRefresh:
		cache.Reset(sess.Refresh())
	case schemaInvalidate:
		cache.Reset(sess.Invalidate(PathAppsConfig))
	case schemaReconnect:
		d.replace(schemaXML("PART-A"), true)
	}
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

	d := &schemaWatchDevice{data: []byte(schemaXML("PART-A"))}
	sess := watchsession.New(d)
	cache := new(SchemaCache)
	app := schemaApp()

	first := requireCoreSchema(t, cache, sess, app)
	if first.Version != 1 || first.PartNumber != "PART-A" {
		t.Fatalf("first schema=%+v", first)
	}

	app.Version = 2
	if second := requireCoreSchema(t, cache, sess, app); second.Version != 2 {
		t.Fatalf("changed app version returned schema version %d", second.Version)
	}

	d.replace(schemaXML("PART-B"), true)

	if other := requireCoreSchema(t, cache, sess, app); other.PartNumber != "PART-B" || other.Version != 2 {
		t.Fatalf("changed device returned schema=%+v", other)
	}

	beforeUnitChange := latest

	d.replace(strings.Replace(schemaXML("PART-B"), "<Id>42</Id>", "<Id>43</Id>", 1), false)

	if _, err := sess.ReadThrough(t.Context(), watchsession.PathDeviceXML, false); err != nil {
		t.Fatal(err)
	}

	requireCoreSchema(t, cache, sess, app)

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

			d := &schemaWatchDevice{data: []byte(schemaXML("PART-A"))}
			sess := watchsession.New(d)
			cache := new(SchemaCache)
			app := schemaApp()
			previous := requireCoreSchema(t, cache, sess, app)
			updated := *previous
			updated.Settings = []ciqstore.Setting{{Key: "field", Type: "text", Default: "after-boundary"}}

			data, err := json.Marshal(&updated)
			if err != nil {
				t.Fatal(err)
			}

			disk := filepath.Join(ciqstore.CacheDir(), schemaTestID+"-v1-PART-A.json")
			if err := os.WriteFile(disk, data, 0o600); err != nil {
				t.Fatal(err)
			}

			schemaBoundary(cache, sess, d, boundary)

			if got := requireCoreSchema(t, cache, sess, app); got.Settings[0].Default != "after-boundary" {
				t.Fatalf("%s retained old in-memory schema: %+v", boundary, got.Settings)
			}
		})
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

			d := &schemaWatchDevice{data: []byte(schemaXML("PART-A"))}
			sess := watchsession.New(d)
			cache := new(SchemaCache)
			app := schemaApp()
			done := make(chan error, 1)

			go func() {
				sc, err := cache.Load(t.Context(), sess, app)
				if err == nil && sc.Settings[0].Default != "old-response" {
					err = fmt.Errorf("unexpected old result: %+v", sc.Settings)
				}

				done <- err
			}()

			<-entered
			schemaBoundary(cache, sess, d, boundary)

			if got := requireCoreSchema(t, cache, sess, app); got.Settings[0].Default != "new-response" {
				t.Fatalf("new generation schema=%+v", got.Settings)
			}

			unblock()

			if err := <-done; err != nil {
				t.Fatal(err)
			}

			if got := requireCoreSchema(t, cache, sess, app); got.Settings[0].Default != "new-response" {
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

			d := &failingMutationDevice{
				schemaWatchDevice: &schemaWatchDevice{data: []byte(schemaXML("PART-A"))},
				failure:           device.ErrDisconnected,
			}
			sess := watchsession.New(d)
			cache := new(SchemaCache)
			requireCoreSchema(t, cache, sess, schemaApp())

			var err error
			if raw {
				_, _, _, err = sess.RawMTPRead(t.Context(), 0x9999, nil)
			} else {
				err = sess.WriteFile(t.Context(), "other-file", nil)
			}

			if !errors.Is(err, device.ErrDisconnected) {
				t.Fatalf("mutation error = %v", err)
			}

			// This fake mutates storage before returning a failure. Restore its XML
			// payload, without Refresh or Reset, so only the attempted mutation invalidates.
			d.replace(schemaXML("PART-A"), false)
			requireCoreSchema(t, cache, sess, schemaApp())

			if metadataCalls != 2 {
				t.Fatalf("mutation retained the old feature cache: metadata calls=%d", metadataCalls)
			}
		})
	}
}
