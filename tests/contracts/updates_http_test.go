package contracts

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestUpdatesHTTPContracts(t *testing.T) { //nolint:paralleltest // replaces process HTTP clients
	for _, test := range []struct { //nolint:paralleltest // cases replace process HTTP clients
		name, method, path, body, mode string
		status                         int
		hash                           string
	}{
		{name: "software", method: http.MethodGet, path: "/api/updates/software", status: http.StatusOK, hash: "959024256dd98d0661ff9eceadd237083ff87e8a7b3b52561d7e77e42f8c2132"},
		{name: "app updates", method: http.MethodGet, path: "/api/updates/apps", status: http.StatusOK, hash: "8786614eb22446f9d2d3a10847ab06008d03d732061a1addf9bdb45b70c0f77c"},
		{name: "inventory", method: http.MethodGet, path: "/api/software", status: http.StatusOK, hash: "edf78db57bfb47cdfb2b44a6aa77bef0ff8b6deccb67a4ce072144cc9a1a9abe"},
		{name: "install", method: http.MethodPost, path: "/api/updates/software/install", body: `{"partNumber":"` + testdump.UpdatePart + `"}`, status: http.StatusOK, hash: "5ddf1ab973ef354b2a98715fd78a3701d495186a12004775d8597575a5dfc12e"},
		{name: "missing part", method: http.MethodPost, path: "/api/updates/software/install", body: `{}`, status: http.StatusBadRequest, hash: "3e2e35f88dcdb03e6e24fd1b384464449b0bd61ea6a932ee139941523bab746d"},
		{name: updateChecksumMode, method: http.MethodPost, path: "/api/updates/software/install", body: `{"partNumber":"` + testdump.UpdatePart + `"}`, mode: updateChecksumMode, status: http.StatusInternalServerError, hash: "411fcc8047caec1e2040b422448692b554e0e7768968bb97a7a2f76f66adf2fc"},
		{name: "software failure", method: http.MethodGet, path: "/api/updates/software", mode: "software", status: http.StatusInternalServerError, hash: "3dcd8896c34045c6daa139b5111586c5347f051f57a741eef40a61907b41ba1e"},
		{name: "app failure", method: http.MethodGet, path: "/api/updates/apps", mode: "app", status: http.StatusOK, hash: "aef77e342753b5dc2dc6fc9bc3bf56c327c9883495ab5781d92b03c6507d99d0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := updates.HTTPClient
			updates.HTTPClient = testdump.UpdateEnvironment(t, test.mode)
			t.Cleanup(func() { updates.HTTPClient = previous })
			isolateContractAccount(t)
			root := testdump.UpdateRoot(t)
			ts := serveContractRoot(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

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
