package contracts

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func newSettingsContractServer(t *testing.T) (*contractServer, string) {
	t.Helper()
	isolateContractAccount(t)
	root := testdump.Root(t)

	return serveContractRoot(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) }), root
}

func TestSettingsHTTPContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	ts, _ := newSettingsContractServer(t)
	for _, test := range []struct {
		name, method, path, body string
		status                   int
		hash                     string
	}{
		{name: "rows", method: http.MethodGet, path: "/api/settings", status: http.StatusOK, hash: "29a4ea8a242fe85b5b06e6753a1da4fb38e79d1ee9dbe195dae667c6c1bdf93d"},
		{name: "settings schema", method: http.MethodGet, path: "/api/schema?mesg=deviceSettings", status: http.StatusOK, hash: "165f663840af697d96d7c5761cd84744d558b14b38191e5b1dcfb6f03d6c48ef"},
		{name: "unknown schema", method: http.MethodGet, path: "/api/schema?mesg=missing", status: http.StatusInternalServerError, hash: "225a75b4eb568acdb3ea2b0701e5deea5f8ff2155bd5c2c75f09650582e243fc"},
		{name: "empty apply", method: http.MethodPost, path: "/api/settings/apply", body: `{"changes":[]}`, status: http.StatusInternalServerError, hash: "90385fd348624d7c50c3e4c8c02b0ef7a240d5b57e9d69eb8c6e782cefde2891"},
		{name: "wrong write route", method: http.MethodPost, path: "/api/settings", body: `{"changes":[]}`, status: http.StatusNotFound, hash: "b16e15764b8bc06c5c3f9f19bc8b99fa48e7894aa5a6ccdad65da49bbf564793"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), test.method, ts.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}

			req.Header.Set("Content-Type", "application/json")

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			data, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != test.status {
				t.Fatalf("status=%d body=%s error=%v", resp.StatusCode, data, err)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}

func TestSettingsHTTPApplyContract(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	ts, root := newSettingsContractServer(t)
	if err := os.Mkdir(filepath.Join(root, "GARMIN", "NewFiles"), 0o750); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/api/settings/apply", strings.NewReader(`{"changes":[{"mesg":"2","field":"58","value":1500}]}`))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status=%d body=%s error=%v", resp.StatusCode, data, err)
	}

	data = testdump.NormalizePendingPath(t, data)
	testdump.CheckDigest(t, data, "afae0508b7947308164da7f5c7881277a79434bc50897f0f1a4821ecf0a5a7b8")
}
