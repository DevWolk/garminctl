package contracts

import (
	"context"
	"net/http"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func newAppsContractServer(t *testing.T) *contractServer {
	t.Helper()
	isolateContractAccount(t)
	dev, _ := testdump.Apps(t)

	return serveContractRoot(t, func(context.Context) (device.Device, error) { return dev, nil })
}

func TestAppsHTTPContracts(t *testing.T) { //nolint:paralleltest // isolates process schema client and cache environment
	testdump.AppEnvironment(t)

	for _, test := range []struct { //nolint:paralleltest // cases share the process schema client and cache environment
		name, method, path, body string
		status                   int
		hash                     string
	}{
		{name: "apps list", method: http.MethodGet, path: "/api/apps", status: http.StatusOK, hash: "8e723d149ae1e3095e6cca8bdc81bc47f0501e4ed15ede9550ddc2c78afe2039"},
		{name: "settings", method: http.MethodGet, path: "/api/apps/" + testdump.AppRef + "/settings", status: http.StatusOK, hash: "be92e32f1c1f74c0602f277dbc71383d464644d328ae819d618c470445fb7b56"},
		{name: "apps schema", method: http.MethodGet, path: "/api/apps/" + testdump.AppRef + "/schema", status: http.StatusOK, hash: "19164b4a8b236683d0f71b1c4e4ac32f655a4be8b68a83393ace1af9e7505370"},
		{name: "set", method: http.MethodPut, path: "/api/apps/" + testdump.AppRef + "/settings", body: `{"values":{"Mode":"Night"}}`, status: http.StatusOK, hash: "07a67de89ebe386fe278b51b606aee899c78ad9756c76d476477893a5de939b8"},
		{name: "pending", method: http.MethodGet, path: "/api/apps/pending", status: http.StatusOK, hash: "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570"},
		{name: "missing app", method: http.MethodGet, path: "/api/apps/missing/settings", status: http.StatusInternalServerError, hash: "2ab5a48351d9bfb3f6a58f40d95e77b9457056fe1c194023fe56a0597c38992c"},
		{name: "read only", method: http.MethodPut, path: "/api/apps/" + testdump.AppRef + "/settings", body: `{"values":{"Locked":2}}`, status: http.StatusInternalServerError, hash: "71c5d8cf62eb22801a92d9c54b5b9a49f3f5efb3f7086e75ff265f3e33bbc9a6"},
		{name: "remove", method: http.MethodDelete, path: "/api/apps/" + testdump.AppRef, status: http.StatusOK, hash: "638ef5b9be4d10043c4ba7412c318c701bb22ceeebbd2c2bb42e965ae08aa419"},
		{name: "invalid PRG", method: http.MethodPost, path: "/api/apps/install", body: "invalid", status: http.StatusInternalServerError, hash: "fa852334449a09207587cb7e1acca1206bdadbed3207689bdaba5cb4e9c47a34"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := newAppsContractServer(t)

			checkHTTPContract(t, ts, test.method, test.path, test.body, test.status, test.hash)
		})
	}
}
