package testdump

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/device"
)

// AppRef identifies the real Easy+ registry entry in OUT.BIN.
const AppRef = "G5RG4154"

// AppEnvironment isolates schema/backup files and prevents real store requests.
// Call before subtests; it changes process globals and cannot run in parallel.
func AppEnvironment(tb testing.TB) {
	tb.Helper()

	cache := tb.TempDir()
	tb.Setenv("XDG_CACHE_HOME", cache)
	tb.Setenv("LOCALAPPDATA", cache)

	previous := ciqstore.HTTPClient
	ciqstore.HTTPClient = &http.Client{Transport: offlineAppTransport{}}

	tb.Cleanup(func() { ciqstore.HTTPClient = previous })

	schema := ciqstore.Schema{
		AppID: "7c993e2b-70b3-459a-bbe0-db48a89af5a8", Version: 23, PartNumber: "006-B4426-00",
		Source: "fixture:app-schema", FetchedAt: "2000-01-02T03:04:05Z",
		Settings: []ciqstore.Setting{
			{Key: "Mode", Label: "Display mode", Type: "select", Options: []ciqstore.Option{{Value: "1", Label: "Day"}, {Value: "2", Label: "Night"}}},
			{Key: "Enabled", Label: "Enabled", Type: "toggle"},
			{Key: "Title", Label: "Title", Type: "text", MaxLength: 12},
			{Key: "Locked", Label: "Read only", Type: "number", ReadOnly: true},
		},
	}

	data, err := json.Marshal(schema)
	if err != nil {
		tb.Fatal(err)
	}

	dir := ciqstore.CacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, schema.AppID+"-v23-"+schema.PartNumber+".json"), data, 0o600); err != nil {
		tb.Fatal(err)
	}
}

type offlineAppTransport struct{}

func (offlineAppTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline app contract fixture")
}

// Apps adds synthetic settings and binary/sidecar files to the real registry.
// The ordered dictionary includes values unknown to the synthetic store schema.
func Apps(tb testing.TB) (device.Device, string) {
	tb.Helper()

	root := Root(tb)
	for _, dir := range []string{"GARMIN/Apps/SETTINGS", "GARMIN/Apps/MEDIA", "GARMIN/Apps/DATA"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o750); err != nil {
			tb.Fatal(err)
		}
	}

	settings, err := ciq.Encode(ciq.Dict{
		{Key: "Mode", Value: int32(1)},
		{Key: "Enabled", Value: true},
		{Key: "Title", Value: "Fixture"},
		{Key: "Locked", Value: int32(7)},
		{Key: "Unknown", Value: []any{int64(1 << 40), nil, float32(1.5)}},
	})
	if err != nil {
		tb.Fatal(err)
	}

	files := map[string][]byte{
		"GARMIN/Apps/SETTINGS/" + AppRef + ".SET": settings,
		"GARMIN/Apps/MEDIA/" + AppRef + ".PRG":    []byte("fixture app binary"),
		"GARMIN/Apps/DATA/" + AppRef + ".DAT":     []byte("fixture data"),
		"GARMIN/Apps/DATA/" + AppRef + ".IDX":     []byte("fixture index"),
		"GARMIN/Apps/DATA/" + AppRef + ".IMT":     []byte("fixture image"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), data, 0o600); err != nil {
			tb.Fatal(err)
		}
	}

	dev, err := device.NewDirFS(root)
	if err != nil {
		tb.Fatal(err)
	}

	return dev, root
}
