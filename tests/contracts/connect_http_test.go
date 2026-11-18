package contracts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

const connectSettingsPath = "/api/cloud/settings"

func TestConnectHTTPContracts(t *testing.T) { //nolint:paralleltest // isolates process cache paths
	for _, test := range []struct { //nolint:paralleltest // cases isolate process cache paths
		name, method, path, body, mode string
		current, normalize             bool
		status                         int
		hash                           string
	}{
		{name: "account without current watch", method: http.MethodGet, path: "/api/cloud/status", status: 200, hash: "6b41f8169921773631d466f8ec45aa8374debb5a2801f2345764131b4ff08426"},
		{name: "registered account", method: http.MethodGet, path: "/api/cloud/status", current: true, status: 200, hash: "a6e81a6a9a7cba80822972b566201db5dd5a51be5fe115ae39d50f2d83c22098"},
		{name: "queue items", method: http.MethodGet, path: "/api/cloud/messages", status: 200, hash: "7a5023cffbd72a76b8d7f4ffe1e9085f7412cdd799d8dfcf951e3f12e36469fb"},
		{name: "empty queue", method: http.MethodGet, path: "/api/cloud/messages", mode: "empty messages", status: 200, hash: "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570"},
		{name: "connect settings", method: http.MethodGet, path: connectSettingsPath, status: 200, hash: "879872dc433ffc2031e01cb0314103b9d0bb83e7d1a5c960ce6bbdb4d4e8937b"},
		{name: "settings unavailable", method: http.MethodGet, path: connectSettingsPath, mode: "settings read failure", status: 500, hash: "11c459d0fa398911bff488de88186c93c38e70a96ba6ccf1c584886679998848"},
		{name: "settings apply", method: http.MethodPut, path: connectSettingsPath, body: `{"changes":{"timeFormat":"time_twelve_hr","language":2}}`, normalize: true, status: 200, hash: "5f8cf35d263d585fcda7bc558ad0eed563907b390741036ecef27ed75852042e"},
		{name: "settings partial pull", method: http.MethodPut, path: connectSettingsPath, body: connectLanguageChangeJSON, mode: contractQueueFailure, status: 200, hash: "65e3cc668140d4da23a4c1ff597ecfd39a7260841841907ab1d5eeb0a09021b8"},
		{name: "settings rejected", method: http.MethodPut, path: connectSettingsPath, body: connectLanguageChangeJSON, mode: "settings put failure", status: 500, hash: "11c459d0fa398911bff488de88186c93c38e70a96ba6ccf1c584886679998848"},
		{name: "empty changes", method: http.MethodPut, path: connectSettingsPath, body: `{"changes":{}}`, status: 400, hash: "dc6d9d2ab481fe6e8dbaca9d6933762be4b28316e3cc4bda4c316433ae32c58f"},
		{name: "forbidden changes", method: http.MethodPut, path: connectSettingsPath, body: `{"changes":{"deviceId":9}}`, status: 500, hash: "d7a73d3bdd243abbcf12951f8b18fe161f57a112b308574f6ac4afcc49f77ef0"},
		{name: "trailing settings JSON", method: http.MethodPut, path: connectSettingsPath, body: `{} {}`, status: 400, hash: contractTrailingJSONHash},
		{name: "sync idle", method: http.MethodGet, path: "/api/cloud/sync", status: 200, hash: "bc12fecb2848889f48121e5944da1372f0e790e9db2d14f5b344999190c61ac1"},
		{name: "login missing fields", method: http.MethodPost, path: "/api/cloud/login", body: `{}`, status: 400, hash: "c79dddd42b3df737d3d743e45e47723dfcf73ce87e87cb40d066f2c53bc2bc0b"},
		{name: "MFA missing code", method: http.MethodPost, path: "/api/cloud/mfa", body: `{}`, status: 400, hash: "6850a67204656f6caf24f85d095b7765e96278f67d8477a6e91f7e6201968dc6"},
		{name: "MFA no pending challenge", method: http.MethodPost, path: "/api/cloud/mfa", body: `{"code":"123456"}`, status: 401, hash: "8bec4802773a8e8b37df3d4102fb436e4b1fabc6ec68ebe9ca0c1cf5d1818319"},
		{name: "logout", method: http.MethodPost, path: "/api/cloud/logout", status: 200, hash: contractOKHash},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.HealthEnvironment(t)
			testdump.ConnectCLIEnvironment(t, test.mode)
			root := testdump.ConnectRoot(t)

			ts := serveContractRoot(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })
			if test.current {
				connectContractWatch(t, ts)
			}

			req, err := http.NewRequestWithContext(t.Context(), test.method, ts.URL+test.path, bytes.NewBufferString(test.body))
			if err != nil {
				t.Fatal(err)
			}

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			data, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != test.status {
				t.Fatalf("status=%d body=%s error=%v", resp.StatusCode, data, err)
			}

			if test.normalize {
				data = testdump.NormalizePendingPath(t, data)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}
