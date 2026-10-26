package ciqstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	testAppID      = "ABC-123"
	testPartNumber = "006-B0425-00"
	testAppRecord  = `{"id":"ABC-123","typeId":"1","latestInternalVersion":12,"latestExternalVersion":"1.2.3","appLocalizations":[{"locale":"fr-FR","name":"French"},{"locale":"en-US","name":"English"}],"developer":{"developerDisplayName":"Test developer"},"compatibleDevicePartNumbers":["006-B0425-00"],"fileSizeInfo":{"byteCountByDeviceTypeId":{"42":1234}},"settingsAvailabilityInfo":{"availabilityByDeviceTypeId":{"42":true}}}`
)

type storeTransport func(*http.Request) (*http.Response, error)

func (f storeTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func replaceHTTPClient(t *testing.T, transport storeTransport) {
	t.Helper()

	previous := HTTPClient
	HTTPClient = &http.Client{Transport: transport}

	t.Cleanup(func() { HTTPClient = previous })
}

func rejectHTTPRequests(t *testing.T) {
	t.Helper()

	var calls atomic.Int32

	replaceHTTPClient(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected HTTP request")
	})
	t.Cleanup(func() {
		if count := calls.Load(); count != 0 {
			t.Errorf("invalid input made %d HTTP requests", count)
		}
	})
}

type storeAPICall struct {
	name string
	call func(context.Context, string) error
}

func TestStoreRejectsInvalidAppIDsBeforeHTTP(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	rejectHTTPRequests(t)

	endpoints := []storeAPICall{
		{name: "LatestVersion", call: func(ctx context.Context, id string) error { _, err := LatestVersion(ctx, id); return err }},
		{name: "AppInfo", call: func(ctx context.Context, id string) error { _, err := AppInfo(ctx, id); return err }},
		{name: "Detail", call: func(ctx context.Context, id string) error { _, err := Detail(ctx, id, "", ""); return err }},
		{name: "Reviews", call: func(ctx context.Context, id string) error { _, err := Reviews(ctx, id, 10); return err }},
		{name: "Fetch", call: func(ctx context.Context, id string) error { _, err := Fetch(ctx, id, 1, testPartNumber); return err }},
		{name: "Load", call: func(ctx context.Context, id string) error { _, err := Load(ctx, id, 1, testPartNumber); return err }},
	}
	for _, endpoint := range endpoints {
		for _, id := range []string{"", "../evil", "id?query=value", "id#fragment", "id/path", "id%2Fpath", "with space", "under_score", "кириллица"} {
			t.Run(endpoint.name+"/"+id, func(t *testing.T) {
				t.Parallel()

				if err := endpoint.call(t.Context(), id); err == nil || !strings.Contains(err.Error(), "app id") {
					t.Fatalf("invalid app ID = %v", err)
				}
			})
		}
	}
}

func TestStoreRejectsInvalidPartNumbersBeforeHTTP(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	rejectHTTPRequests(t)

	endpoints := []storeAPICall{
		{name: "Fetch", call: func(ctx context.Context, part string) error { _, err := Fetch(ctx, testAppID, 1, part); return err }},
		{name: "Load", call: func(ctx context.Context, part string) error { _, err := Load(ctx, testAppID, 1, part); return err }},
		{name: "DeviceTypeID", call: func(ctx context.Context, part string) error { _, err := DeviceTypeID(ctx, part); return err }},
		{name: "Detail", call: func(ctx context.Context, part string) error { _, err := Detail(ctx, testAppID, part, ""); return err }},
		{name: "Browse", call: func(ctx context.Context, part string) error {
			_, err := Browse(ctx, StoreQuery{PartNumber: part})
			return err
		}},
	}
	for _, endpoint := range endpoints {
		for _, part := range []string{"../evil", "id?query=value", "id/path", "with space"} {
			t.Run(endpoint.name+"/"+part, func(t *testing.T) {
				t.Parallel()

				if err := endpoint.call(t.Context(), part); err == nil || !strings.Contains(err.Error(), "part number") {
					t.Fatalf("invalid part number = %v", err)
				}
			})
		}
	}

	for _, endpoint := range endpoints[:3] {
		t.Run(endpoint.name+"/missing required part", func(t *testing.T) {
			t.Parallel()

			if err := endpoint.call(t.Context(), ""); err == nil {
				t.Fatal("missing required part number accepted")
			}
		})
	}
}

func storeResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: strconv.Itoa(status) + " " + http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}
}

func assertStoreRequest(t *testing.T, req *http.Request, path, query, agent, accept string) {
	t.Helper()

	if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "apps.garmin.com" ||
		req.URL.Path != "/api/appsLibraryExternalServices/api/asw"+path || req.URL.RawQuery != query ||
		req.Header.Get("User-Agent") != agent || req.Header.Get("Accept") != accept {
		t.Fatalf("unexpected store request: %s %s, headers %v", req.Method, req.URL, req.Header)
	}
}

func TestLatestVersionRequest(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		assertStoreRequest(t, req, "/apps/"+testAppID, "", "", "")
		return storeResponse(req, http.StatusOK, testAppRecord), nil
	})

	if version, err := LatestVersion(t.Context(), testAppID); err != nil || version != 12 {
		t.Fatalf("LatestVersion = %d, %v", version, err)
	}
}

func TestAppInfoRequestKeepsFirstLocalization(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		assertStoreRequest(t, req, "/apps/"+testAppID, "", "", "")
		return storeResponse(req, http.StatusOK, testAppRecord), nil
	})

	info, err := AppInfo(t.Context(), testAppID)
	if err != nil || info.Name != "French" || info.DeveloperName != "Test developer" || info.LatestInternalVersion != 12 {
		t.Fatalf("AppInfo = %+v, %v", info, err)
	}
}

func TestDetailOptionalDeviceContext(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	for _, part := range []string{"", testPartNumber} { //nolint:paralleltest // cases replace the shared client and run sequentially
		t.Run("part/"+part, func(t *testing.T) {
			replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
				assertStoreRequest(t, req, "/apps/"+testAppID, "countryCode=US", "Mozilla/5.0 garminctl", "application/json")
				return storeResponse(req, http.StatusOK, testAppRecord), nil
			})

			detail, err := Detail(t.Context(), testAppID, part, "42")
			if err != nil || detail.Name != "English" || detail.SizeBytes != 1234 || detail.HasSettings == nil || !*detail.HasSettings {
				t.Fatalf("Detail = %+v, %v", detail, err)
			}
		})
	}
}

func TestBrowseWithoutDevice(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		assertStoreRequest(t, req, "/apps", "appType=watchface&countryCode=US&pageSize=24&startPageIndex=0", "Mozilla/5.0 garminctl", "application/json")
		return storeResponse(req, http.StatusOK, "[]"), nil
	})

	page, err := Browse(t.Context(), StoreQuery{})
	if err != nil || page.Apps == nil || page.Total != -1 {
		t.Fatalf("Browse = %+v, %v", page, err)
	}
}

func TestReviewsDefaults(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		assertStoreRequest(t, req, "/apps/"+testAppID+"/reviews", "pageSize=10&startPageIndex=0", "Mozilla/5.0 garminctl", "application/json")
		return storeResponse(req, http.StatusOK, "[]"), nil
	})

	reviews, err := Reviews(t.Context(), testAppID, 0)
	if err != nil || reviews == nil || len(reviews) != 0 {
		t.Fatalf("Reviews = %+v, %v", reviews, err)
	}
}

func TestDeviceTypeRequest(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		assertStoreRequest(t, req, "/deviceTypes", "", "Mozilla/5.0 garminctl", "application/json")
		return storeResponse(req, http.StatusOK, `[{"id":"42","partNumber":"006-b0425-00"}]`), nil
	})

	if id, err := DeviceTypeID(t.Context(), testPartNumber); err != nil || id != "42" {
		t.Fatalf("DeviceTypeID = %q, %v", id, err)
	}
}

func TestStoreHTTPErrorContracts(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	tests := []struct {
		name string
		call func(context.Context) error
		want string
	}{
		{name: "LatestVersion", call: func(ctx context.Context) error { _, err := LatestVersion(ctx, testAppID); return err }, want: "GET " + storeAPI + "/apps/" + testAppID + ": 409 Conflict"},
		{name: "AppInfo", call: func(ctx context.Context) error { _, err := AppInfo(ctx, testAppID); return err }, want: "store answered 409 Conflict"},
		{name: "Detail", call: func(ctx context.Context) error { _, err := Detail(ctx, testAppID, "", ""); return err }, want: "store answered 409 Conflict: store busy"},
		{name: "Reviews", call: func(ctx context.Context) error { _, err := Reviews(ctx, testAppID, 10); return err }, want: "store answered 409 Conflict: store busy"},
	}
	for _, tt := range tests { //nolint:paralleltest // each case installs its own shared HTTP client
		t.Run(tt.name, func(t *testing.T) {
			replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
				return storeResponse(req, http.StatusConflict, `{"message":"store busy"}`), nil
			})

			if err := tt.call(t.Context()); err == nil || err.Error() != tt.want {
				t.Fatalf("HTTP error = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestAppRecordDecoderContracts(t *testing.T) { //nolint:paralleltest // replaces the package's shared HTTP client
	response := "{}"

	replaceHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		return storeResponse(req, http.StatusOK, response), nil
	})

	if _, err := LatestVersion(t.Context(), testAppID); err == nil || err.Error() != "store returned no version" {
		t.Fatalf("missing version = %v", err)
	}

	if info, err := AppInfo(t.Context(), testAppID); err != nil || info.LatestInternalVersion != 0 {
		t.Fatalf("AppInfo without version = %+v, %v", info, err)
	}

	response = testAppRecord + "{}"

	if _, err := LatestVersion(t.Context(), testAppID); err != nil {
		t.Fatalf("LatestVersion no longer accepts its first JSON value: %v", err)
	}

	if _, err := AppInfo(t.Context(), testAppID); err != nil {
		t.Fatalf("AppInfo no longer accepts its first JSON value: %v", err)
	}

	if _, err := Detail(t.Context(), testAppID, "", ""); err == nil {
		t.Fatal("Detail must retain its whole-body JSON decoding")
	}

	response = `{"latestInternalVersion":12,"padding":"` + strings.Repeat("x", 1<<20) + `"}`

	if _, err := LatestVersion(t.Context(), testAppID); err == nil {
		t.Fatal("LatestVersion no longer enforces its 1 MiB response limit")
	}

	if info, err := AppInfo(t.Context(), testAppID); err != nil || info.LatestInternalVersion != 12 {
		t.Fatalf("AppInfo lost its separate 4 MiB limit: %+v, %v", info, err)
	}
}
