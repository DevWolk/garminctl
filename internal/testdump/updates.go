package testdump

import (
	"crypto/md5" //nolint:gosec // fixture for the checksum supplied by Garmin's manifest
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciqstore"
)

// UpdatePayload is synthetic data written only to a temporary directory watch.
const UpdatePayload = "synthetic firmware"

// UpdatePart is the primary firmware item in the synthetic manifest.
const UpdatePart = "006-FIXTURE-FW"

// UpdateEnvironment returns an offline update client and temporarily replaces
// the store client. Modes software, app and checksum fail one independent step.
// The caller supplies this client to its update owner; tests must be sequential.
func UpdateEnvironment(tb testing.TB, mode string) *http.Client {
	tb.Helper()

	client := &http.Client{Transport: updateTransport{mode: mode}}
	previous := ciqstore.HTTPClient
	ciqstore.HTTPClient = client

	tb.Cleanup(func() { ciqstore.HTTPClient = previous })

	return client
}

// UpdateRoot adds the existing binary software-inventory fixture to a dump.
func UpdateRoot(tb testing.TB) string {
	tb.Helper()
	root := Root(tb)

	data, err := os.ReadFile(filepath.Join(repositoryRoot(tb), "internal", "fit", "testdata", "device.fit"))
	if err != nil {
		tb.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "GARMIN", "Device.fit"), data, 0o600); err != nil { //nolint:gosec // fixed fixture path under Root's temporary directory
		tb.Fatal(err)
	}

	return root
}

type updateTransport struct{ mode string }

func (f updateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := http.StatusOK, ""

	switch {
	case req.URL.String() == "https://omt.garmin.com/api/device-software/v1/updates" && req.Method == http.MethodPost:
		var input map[string]string
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil || !strings.Contains(input["GarminDeviceXml"], "<Device") {
			return nil, fmt.Errorf("invalid fixture XML request: %w", err)
		}

		sum := md5.Sum([]byte(UpdatePayload)) //nolint:gosec // fixture manifest integrity checksum
		body = fmt.Sprintf(`{"updates":[{"partNumber":"006-FIXTURE-DATA","productName":"Fixture data","version":"1.00","dataType":"Database","installation":{"isProductInstalled":true}},{"partNumber":"%s","productName":"Fixture firmware","version":"200","displayVersion":"2.00","isPrimaryFirmware":true,"dataType":"Firmware","severity":"Recommended","releaseNotes":[{"changeLog":["  Fixture change  "]}],"releaseDate":"2000-01-02","eulaUrl":"https://fixture.invalid/eula","deliverable":{"downloadUrl":"https://fixture.invalid/firmware","md5":"%s","sizeInBytes":%d},"installation":{"path":"\\Garmin\\RemoteSW\\","fileName":"GUPDATE.GCD","isRestartRequired":false}}]}`, UpdatePart, strings.ToUpper(hex.EncodeToString(sum[:])), len(UpdatePayload))

		if f.mode == "software" {
			status = http.StatusServiceUnavailable
		}
	case req.URL.String() == "https://fixture.invalid/firmware" && req.Method == http.MethodGet:
		body = UpdatePayload
		if f.mode == "checksum" {
			body = strings.Repeat("x", len(UpdatePayload))
		}
	case req.URL.Host == "apps.garmin.com" && strings.HasPrefix(req.URL.Path, "/api/appsLibraryExternalServices/api/asw/apps/") && req.Method == http.MethodGet:
		body = `{"latestInternalVersion":24,"latestExternalVersion":"2.4","appLocalizations":[{"locale":"en-US","name":"Fixture app"}]}`
		if f.mode == "app" && strings.HasSuffix(req.URL.Path, StoreAppID) {
			status, body = http.StatusConflict, `{"message":"fixture app unavailable"}`
		}
	default:
		return nil, fmt.Errorf("unexpected update fixture request: %s %s", req.Method, req.URL)
	}

	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
