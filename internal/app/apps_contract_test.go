package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
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
		{name: "list", method: http.MethodGet, path: "/api/apps", status: http.StatusOK, hash: "173f4927f9d6d4c9e169cedf2f52a3cad075378ea327cdf43e9fa3c70e4a5803"},
		{name: "settings", method: http.MethodGet, path: "/api/apps/G5RG4154/settings", status: http.StatusOK, hash: "a0c762b6dc5d77d706e3ccef45e4303fa1a51a96a2314738df054364a2e42445"},
		{name: "schema", method: http.MethodGet, path: "/api/apps/G5RG4154/schema", status: http.StatusOK, hash: "8bed91e1de68d4fc8675a505a09840038ba51bec101a17b740419919c55277e4"},
		{name: "set", method: http.MethodPut, path: "/api/apps/G5RG4154/settings", body: `{"values":{"Mode":"Night"}}`, status: http.StatusOK, hash: "d68a9cda0c60617d86b0122b72c78a840e39b565141d4782fd50cb62aeacd724"},
		{name: "pending", method: http.MethodGet, path: "/api/apps/pending", status: http.StatusOK, hash: "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570"},
		{name: "missing app", method: http.MethodGet, path: "/api/apps/missing/settings", status: http.StatusInternalServerError, hash: "2ab5a48351d9bfb3f6a58f40d95e77b9457056fe1c194023fe56a0597c38992c"},
		{name: "read only", method: http.MethodPut, path: "/api/apps/G5RG4154/settings", body: `{"values":{"Locked":2}}`, status: http.StatusInternalServerError, hash: "71c5d8cf62eb22801a92d9c54b5b9a49f3f5efb3f7086e75ff265f3e33bbc9a6"},
		{name: "remove", method: http.MethodDelete, path: "/api/apps/G5RG4154", status: http.StatusOK, hash: "64059f06dc7e0571487165acca200788f71fedb9f93c5af034c958b1f1f6d7eb"},
		{name: "invalid PRG", method: http.MethodPost, path: "/api/apps/install", body: "invalid", status: http.StatusInternalServerError, hash: "fa852334449a09207587cb7e1acca1206bdadbed3207689bdaba5cb4e9c47a34"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := newAppsContractServer(t)

			checkHTTPContract(t, ts, test.method, test.path, test.body, test.status, test.hash)
		})
	}
}
