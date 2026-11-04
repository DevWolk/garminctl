package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func newWatchContractServer(t *testing.T) *httptest.Server {
	t.Helper()

	dev, _ := testdump.Watch(t)
	s := newServer(t.Context(), func(context.Context) (device.Device, error) { return dev, nil }, false)
	s.cloud = gc.New(filepath.Join(t.TempDir(), "account.json"))
	ts := httptest.NewUnstartedServer(nil)

	h, err := s.handler(ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	ts.Config.Handler = h
	ts.Start()
	t.Cleanup(func() {
		ts.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := s.mcp.Close(ctx); err != nil {
			t.Error(err)
		}

		if err := s.runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	return ts
}

func TestWatchHTTPContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, method, path, body string
		status                   int
		hash                     string
	}{
		{name: "files", method: http.MethodGet, path: "/api/files?path=GARMIN", status: http.StatusOK, hash: "a533e1efab112a20a105ca4000262a1f33b163a36dd1a674b130deec5a93ad0f"},
		{name: "decode", method: http.MethodGet, path: "/api/decode?path=GARMIN/Settings/Settings.fit", status: http.StatusOK, hash: "772b34787f347e3621f449f3ed851018a8394a288e295985158278d0cb3841f0"},
		{name: "connect", method: http.MethodPost, path: "/api/connect", status: http.StatusOK, hash: "3f077a08c442671570f47d4dfdc7e84f851f0d1245bf38b80d0cbf3efdda1259"},
		{name: "status idle", method: http.MethodGet, path: "/api/status", status: http.StatusOK, hash: "96be9bd16c393690286bf3382b7f5f22aacdfa584078ce6f812abdb43b520ebd"},
		{name: "status connect", method: http.MethodGet, path: "/api/status?connect=1", status: http.StatusOK, hash: "7e410e5ae696f8f9d78de6aea3b548c0a3ce62784cb4b9e5ff9d0c1798e58845"},
		{name: "refresh", method: http.MethodPost, path: "/api/refresh", status: http.StatusOK, hash: contractOKHash},
		{name: "put", method: http.MethodPost, path: "/api/file?path=GARMIN/new.bin", body: "new data", status: http.StatusOK, hash: "4473d6c9c3762acd55da7c78b079af9869bc17658f5bd5212ae124126b56f859"},
		{name: "delete", method: http.MethodDelete, path: "/api/file?path=GARMIN/notes.txt", status: http.StatusOK, hash: contractOKHash},
		{name: "missing download", method: http.MethodGet, path: "/api/file?path=GARMIN/missing", status: http.StatusNotFound, hash: "038a1831b21c559420ce87cf7478c6581b7cdc07d9249ad46431c2bb9b860914"},
		{name: "invalid delete", method: http.MethodDelete, path: "/api/file?path=../outside", status: http.StatusBadRequest, hash: "bd81575f7f5a970b099a407c54ca27a7aad3bb1690319a6542ad8f0d8c8cb86c"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ts := newWatchContractServer(t)

			req, err := http.NewRequestWithContext(t.Context(), test.method, ts.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}

			response, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			data, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != test.status {
				t.Fatalf("status=%d body=%s error=%v", response.StatusCode, data, err)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}

func TestWatchHTTPDownloadBytesAndHeaders(t *testing.T) {
	t.Parallel()

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
