package apps

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const schemaRetainedFallback = "fallback schema"

func TestOwnSettingsRetentionDoesNotSurviveAnotherCacheBoundary(t *testing.T) { //nolint:paralleltest // Cases replace the shared schema HTTP client and cache environment.
	for _, boundary := range []string{schemaRefresh, schemaInvalidate, schemaReconnect, "app version", "part number", "unit ID", schemaRetainedFallback} { //nolint:paralleltest // Cases share process-wide schema client/configuration.
		t.Run(boundary, func(t *testing.T) {
			var metadata atomic.Int32

			schemaCacheHTTP(t, func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "/appSettings2/") {
					if boundary == schemaRetainedFallback && strings.Contains(req.URL.Path, "/versions/1/") {
						return nil, errors.New("fixture installed schema unavailable")
					}

					return schemaFormResponse(req, "retained-contract"), nil
				}

				metadata.Add(1)

				if boundary == schemaRetainedFallback {
					return syncResponse(req, []byte(`{"latestInternalVersion":2}`)), nil
				}

				return syncResponse(req, []byte(`{"latestInternalVersion":1}`)), nil
			})

			d := &schemaWatchDevice{data: []byte(schemaXML("PART-A"))}
			sess, cache, app := watchsession.New(d), new(SchemaCache), schemaApp()

			loaded := requireCoreSchema(t, cache, sess, app)
			if boundary == schemaRetainedFallback && loaded.Version != 2 {
				t.Fatal("fixture did not load a newer fallback schema")
			}

			cache.Reset(sess.Invalidate("GARMIN/Apps/SETTINGS/schema.SET"))
			cache.Retain(t.Context(), sess, app, loaded)

			before := metadata.Load()

			if got := requireCoreSchema(t, cache, sess, app); got != loaded || metadata.Load() != before {
				t.Fatal("own settings readback fetched or replaced its schema")
			}

			crossRetainedSchemaBoundary(t, boundary, sess, cache, d, app)
			requireCoreSchema(t, cache, sess, app)

			if metadata.Load() != before+1 {
				t.Fatalf("retained entry crossed %s: metadata requests=%d, expected=%d", boundary, metadata.Load(), before+1)
			}
		})
	}
}

func crossRetainedSchemaBoundary(t *testing.T, boundary string, sess *watchsession.Session, cache *SchemaCache, d *schemaWatchDevice, app *App) {
	t.Helper()

	switch boundary {
	case "app version":
		app.Version++
	case "part number":
		d.replace(schemaXML("PART-B"), true)
	case "unit ID":
		d.replace(strings.Replace(schemaXML("PART-A"), "<Id>42</Id>", "<Id>43</Id>", 1), true)
	case schemaRefresh, schemaInvalidate, schemaReconnect:
		schemaBoundary(cache, sess, d, boundary)
	case schemaRetainedFallback:
		schemaBoundary(cache, sess, d, schemaRefresh)
	default:
		t.Fatalf("unknown retained schema boundary: %s", boundary)
	}
}
