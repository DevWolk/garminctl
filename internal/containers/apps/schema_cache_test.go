package apps

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const schemaTestID = "schema-app"

type syncTransport func(*http.Request) (*http.Response, error)

func (f syncTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func syncResponse(req *http.Request, body []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}
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

func schemaXML(part string) string {
	return `<Device><Id>42</Id><Model><PartNumber>` + part + `</PartNumber></Model></Device>`
}
func schemaApp() *App { return &App{ID: schemaTestID, FileName: "schema.PRG", Version: 1} }

type cacheTestDevice struct {
	device.Device
	data    []byte
	onCount func()
}

func (d *cacheTestDevice) Reconnects() int {
	if d.onCount != nil {
		d.onCount()
	}

	return 0
}

func (d *cacheTestDevice) Read(context.Context, string) ([]byte, error) {
	return bytes.Clone(d.data), nil
}

func requireCoreSchema(t *testing.T, cache *SchemaCache, sess *watchsession.Session, app *App) *ciqstore.Schema {
	t.Helper()

	sc, err := cache.Load(t.Context(), sess, app)
	if err != nil {
		t.Fatal(err)
	}

	if len(sc.Settings) != 1 {
		t.Fatalf("schema settings=%+v", sc.Settings)
	}

	return sc
}

func TestAppSchemaFallbackRemainsCachedForRequestedVersion(t *testing.T) { //nolint:paralleltest // replaces the shared schema HTTP client and cache environment
	var latest, forms atomic.Int32

	schemaCacheHTTP(t, func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/appSettings2/") {
			latest.Add(1)
			return syncResponse(req, []byte(`{"latestInternalVersion":2}`)), nil
		}

		forms.Add(1)

		resp := schemaFormResponse(req, "fallback-schema")
		if strings.Contains(req.URL.Path, "/versions/1/") {
			resp.StatusCode = http.StatusNotFound
		}

		return resp, nil
	})

	d := &cacheTestDevice{data: []byte(schemaXML("PART-A"))}

	var cache SchemaCache

	sess := watchsession.New(d)
	d.onCount = func() {
		if !cache.mu.TryLock() {
			t.Error("schema cache sampled transport while holding cache mutex")
			return
		}
		cache.mu.Unlock()
	}

	for range 2 {
		if got := requireCoreSchema(t, &cache, sess, schemaApp()); got.Version != 2 {
			t.Fatalf("fallback version=%d", got.Version)
		}
	}

	if latest.Load() != 1 || forms.Load() != 2 {
		t.Fatalf("cache hit retried fallback: latest=%d forms=%d", latest.Load(), forms.Load())
	}
}

func TestSchemaCacheLatePublicationKeepsNewerGeneration(t *testing.T) {
	t.Parallel()

	for _, late := range []string{"lookup", "publish", "reset", "same-generation reset"} {
		t.Run(late, func(t *testing.T) {
			t.Parallel()

			var cache SchemaCache

			key := appSchemaKey{appID: "app"}
			newer := &ciqstore.Schema{Version: 2}
			cache.store(key, 2, newer)

			// A caller sampled generation 1 before another caller published 2.
			// Complete its remaining in-memory operation after that publication.
			switch late {
			case "lookup":
				if got := cache.load(key, 1); got != nil {
					t.Fatalf("old lookup returned newer entry: %+v", got)
				}
			case "publish":
				cache.store(key, 1, &ciqstore.Schema{Version: 1})
			case "reset":
				cache.Reset(1)
			case "same-generation reset":
				cache.Reset(2)
			}

			if got := cache.load(key, 2); got != newer {
				t.Fatalf("late %s replaced/cleared current schema: %+v", late, got)
			}

			if got := cache.load(key, 3); got != nil {
				t.Fatalf("new generation reused old schema: %+v", got)
			}
		})
	}
}
