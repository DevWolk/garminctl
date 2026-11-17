package contracts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/store"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestStoreHTTPContracts(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	for _, test := range []struct { //nolint:paralleltest // cases replace the process store client
		name, path, fail string
		connected        bool
		status           int
		hash             string
	}{
		{name: "offline list", path: "/api/store", status: http.StatusOK, hash: "95d681bd35e35d33f804cfd6cb0f88a6f20ac85fe4b54549692562449e79f7a7"},
		{name: "connected list", path: "/api/store", connected: true, status: http.StatusOK, hash: "662d85149cad48c546428eba3d9663ff3a29cbcd7fc805ec1323e3a330867a52"},
		{name: "offline detail", path: "/api/store/apps/" + testdump.StoreAppID, status: http.StatusOK, hash: "258f7126c2d524dbc0bfa95590cf780c7125e6a2cb544c256b1bd55d9000681a"},
		{name: "connected detail", path: "/api/store/apps/" + testdump.StoreAppID, connected: true, status: http.StatusOK, hash: "981dcdd96deeb4f835b483f15c50ca3950f6bfe4e4d4ff12d7c8f0d0cda438fd"},
		{name: storeFilteredSearchCase, path: "/api/store?q=face&type=watchface&page=2", connected: true, status: http.StatusOK, hash: "2edff596664955fb2ad6c6decf4d9ca8e85517e02bc2968d1601d388596874e9"},
		{name: "invalid page defaults", path: "/api/store?page=bad", status: http.StatusOK, hash: "95d681bd35e35d33f804cfd6cb0f88a6f20ac85fe4b54549692562449e79f7a7"},
		{name: "reviews unavailable", path: "/api/store/apps/" + testdump.StoreAppID, connected: true, fail: "/apps/" + testdump.StoreAppID + "/reviews", status: http.StatusOK, hash: "53f5201cda75bae151292658aa82b70e42c7615c1b3ddf89e0022a5230245a57"},
		{name: "browse unavailable", path: "/api/store", fail: "/apps", status: http.StatusInternalServerError, hash: "4fe69e6b27cda04068fdc954a0aadae95721dd7bf4aebac4586780ac2794dbf4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := testdump.StoreEnvironment(t, test.fail)
			ts, _, opens := newStoreContractServer(t, test.connected)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+test.path, nil)
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

			wantOpens := int32(0)
			if test.connected {
				wantOpens = 1
			}

			if opens.Load() != wantOpens {
				t.Fatalf("store opened a watch: opens=%d want=%d", opens.Load(), wantOpens)
			}

			if test.name == storeFilteredSearchCase {
				fixture.CheckPage(t, 24, 48)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}

func TestStoreHTTPIgnoresUnavailableXML(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	testdump.StoreEnvironment(t, "")
	ts, root, opens := newStoreContractServer(t, true)

	// Mutate through the same public endpoint that invalidates Session caches.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, ts.URL+"/api/file?path=GARMIN/GarminDevice.xml", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete XML status=%d root=%s", resp.StatusCode, root)
	}

	var view store.View

	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/store", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("store without XML status=%d", response.StatusCode)
	}

	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}

	if len(view.Apps) != 2 || !view.Apps[0].Installed || view.Apps[0].HasSettings != nil || opens.Load() != 1 {
		t.Fatalf("without XML: view=%+v opens=%d", view, opens.Load())
	}
}

func newStoreContractServer(t *testing.T, connected bool) (*contractServer, string, *atomic.Int32) {
	t.Helper()
	isolateContractAccount(t)
	root := testdump.Root(t)
	opens := new(atomic.Int32)

	ts := serveContractRoot(t, func(context.Context) (device.Device, error) {
		opens.Add(1)
		return device.NewDirFS(root)
	})
	if connected {
		connectContractWatch(t, ts)
	}

	return ts, root, opens
}
