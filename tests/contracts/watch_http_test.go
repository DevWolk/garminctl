package contracts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func newWatchContractServer(t *testing.T) *contractServer {
	t.Helper()
	isolateContractAccount(t)

	dev, root := testdump.Watch(t)
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}

	return serveContractRoot(t, func(context.Context) (device.Device, error) { return testdump.OpenWatch(root) })
}

func TestWatchHTTPContracts(t *testing.T) { //nolint:paralleltest // the real entry point reads isolated process account paths.
	for _, test := range []struct { //nolint:paralleltest // each case isolates process account paths.
		name, method, path, body string
		status                   int
		hash                     string
	}{
		{name: "files", method: http.MethodGet, path: "/api/files?path=GARMIN", status: http.StatusOK, hash: "2f28549f93e9d3bca528d84ab47104764b581bc75990388be610db1b7a2748b2"},
		{name: "decode", method: http.MethodGet, path: "/api/decode?path=GARMIN/Settings/Settings.fit", status: http.StatusOK, hash: "b3df8934c98c791f9c87cb4212d1c17826eb0ec8b9f4795e711395ee853ea90c"},
		{name: "connect", method: http.MethodPost, path: "/api/connect", status: http.StatusOK, hash: "d447e0e6888eed5d5810ad3bcccaa499dc0616fbff4cf4ee592f2f6676750f05"},
		{name: "status idle", method: http.MethodGet, path: "/api/status", status: http.StatusOK, hash: "96be9bd16c393690286bf3382b7f5f22aacdfa584078ce6f812abdb43b520ebd"},
		{name: "status connect", method: http.MethodGet, path: "/api/status?connect=1", status: http.StatusOK, hash: "54517b442f1b8a653e7a3aecf1405f9d03827f8a6d22efe090498d33f12c93f5"},
		{name: "refresh", method: http.MethodPost, path: "/api/refresh", status: http.StatusOK, hash: contractOKHash},
		{name: "put", method: http.MethodPost, path: "/api/file?path=GARMIN/new.bin", body: "new data", status: http.StatusOK, hash: "4473d6c9c3762acd55da7c78b079af9869bc17658f5bd5212ae124126b56f859"},
		{name: "delete", method: http.MethodDelete, path: "/api/file?path=GARMIN/notes.txt", status: http.StatusOK, hash: contractOKHash},
		{name: "missing download", method: http.MethodGet, path: "/api/file?path=GARMIN/missing", status: http.StatusNotFound, hash: "038a1831b21c559420ce87cf7478c6581b7cdc07d9249ad46431c2bb9b860914"},
		{name: "invalid delete", method: http.MethodDelete, path: "/api/file?path=../outside", status: http.StatusBadRequest, hash: "bd81575f7f5a970b099a407c54ca27a7aad3bb1690319a6542ad8f0d8c8cb86c"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := newWatchContractServer(t)

			checkHTTPContract(t, ts, test.method, test.path, test.body, test.status, test.hash)
		})
	}
}

func TestWatchHTTPDownloadBytesAndHeaders(t *testing.T) { //nolint:paralleltest // the real entry point reads isolated process account paths.
	ts := newWatchContractServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/file?path=garmin/sample.bin", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(data, []byte{0, 1, 2, 3}) {
		t.Fatalf("download status=%d bytes=%x error=%v", response.StatusCode, data, err)
	}

	for name, want := range map[string]string{
		"Content-Type":           "application/octet-stream",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    `attachment; filename="sample.bin"`,
	} {
		if got := response.Header.Get(name); got != want {
			t.Errorf("%s=%q want %q", name, got, want)
		}
	}
}
