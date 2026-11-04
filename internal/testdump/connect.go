package testdump

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/gc"
)

// ConnectDeviceXML advertises only the synthetic Connect transfer locations.
const ConnectDeviceXML = `<Device><Model><PartNumber>006-FIXTURE-00</PartNumber><SoftwareVersion>1234</SoftwareVersion><Description>Fixture watch</Description></Model><Id>42</Id><MassStorageMode><DataType><Name>FIT_TYPE_4</Name><File><Location><Path>GARMIN/Activity</Path><FileExtension>fit</FileExtension></Location><TransferDirection>OutputFromUnit</TransferDirection></File></DataType><DataType><Name>FIT_TYPE_41</Name><File><Location><Path>GARMIN/NewFiles</Path><FileExtension>fit</FileExtension></Location><TransferDirection>InputToUnit</TransferDirection></File></DataType></MassStorageMode></Device>`

// ConnectUploadPath is the single synthetic recorded file in ConnectRoot.
const ConnectUploadPath = "GARMIN/Activity/fixture.fit"

// ConnectUploadPayload is opaque synthetic data used only for upload requests.
const ConnectUploadPayload = "synthetic recorded activity"

// ConnectDownloadPayload is opaque synthetic data delivered to NewFiles.
const ConnectDownloadPayload = "synthetic queued settings"

// ConnectSettingsJSON exercises settings types, options, sorting and exclusions.
const ConnectSettingsJSON = `{"deviceId":42,"timeFormat":"time_twelve_hr","dateFormat":"date_day_month","measurementUnits":"metric","mountingSide":"LEFT","language":1,"startOfWeek":"MONDAY","dataRecording":"SMART","autoUpload":true,"autoSyncStepsBeforeSync":1000,"alarms":[{"enabled":true,"time":"07:30"}],"connectIQ":{"autoUpdate":true},"unsupportedValue":null,"emptyObject":{},"supportedLanguages":[{"id":1,"name":"English"},{"id":2,"name":"Polish"}],"supportedAlarmModes":["DAILY"],"enabledScreens":[1],"visibleScreens":[2]}`

// ConnectRawPath returns a small JSON object for CLI raw-request contracts.
const ConnectRawPath = "/fixture-connect-json"

// ConnectRoot supplies a small watch tree for offline Connect contracts.
func ConnectRoot(tb testing.TB) string {
	tb.Helper()
	root := Root(tb)
	AddConnect(tb, root)

	return root
}

// AddConnect installs synthetic transfer metadata and a stable recorded file.
func AddConnect(tb testing.TB, root string) {
	tb.Helper()

	for _, dir := range []string{"GARMIN/NewFiles", "GARMIN/Activity"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o750); err != nil {
			tb.Fatal(err)
		}
	}

	stamp := time.Date(2000, time.January, 2, 3, 4, 5, 0, time.UTC)
	files := map[string]string{
		"GARMIN/GarminDevice.xml": ConnectDeviceXML,
		ConnectUploadPath:         ConnectUploadPayload,
	}

	for name, data := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
			tb.Fatal(err)
		}

		if err := os.Chtimes(file, stamp, stamp); err != nil {
			tb.Fatal(err)
		}
	}
}

// ConnectClient supplies synthetic saved authentication and rejects unlisted HTTP.
// Modes are success (also the default), empty messages, settings read failure,
// settings put failure, queue failure, download failure, ack failure,
// duplicate upload, upload failure, and unknown destination.
func ConnectClient(tb testing.TB, mode string) *gc.Client {
	tb.Helper()
	client := workoutClient(tb, filepath.Join(tb.TempDir(), "account.json"), "")
	client.HTTP.Transport = connectTransport{mode: mode}

	return client
}

// ConnectCLIEnvironment isolates CLI account/cache paths and default transport.
// Tests using this helper must not run in parallel.
func ConnectCLIEnvironment(t *testing.T, mode string) {
	t.Helper()
	accountCLIEnvironment(t, connectTransport{mode: mode})
}

func accountCLIEnvironment(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())

	store := gc.DefaultStorePath()
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}

	client := workoutClient(t, store, "")
	client.HTTP.Transport = transport
	previous := http.DefaultTransport
	http.DefaultTransport = client.HTTP.Transport

	t.Cleanup(func() { http.DefaultTransport = previous })
}

type connectTransport struct{ mode string }

func (f connectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "connectapi.garmin.com" {
		return nil, fmt.Errorf("unexpected Connect fixture host: %s", req.URL)
	}

	key := req.Method + " " + req.URL.Path

	body, ok := connectBodies()[key]
	if !ok || !connectQueryMatches(req) {
		return nil, fmt.Errorf("unexpected Connect fixture request: %s %s", req.Method, req.URL)
	}

	var status int
	if connectFailureRequest(f.mode) == key {
		status, body = http.StatusServiceUnavailable, "synthetic Connect unavailable"
	} else {
		status, body = f.response(req.URL.Path, body)
	}

	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func connectBodies() map[string]string {
	return map[string]string{
		"GET /userprofile-service/socialProfile":                    `{"displayName":"fixture-connect","fullName":"Fixture Person","userName":"fixture.person"}`,
		"GET /device-service/deviceregistration/device/name/42":     `"Fixture watch"`,
		"GET /device-service/deviceservice/device-info/settings/42": ConnectSettingsJSON,
		"PUT /device-service/deviceservice/device-info/settings/42": `{}`,
		"GET /device-service/devicemessage/messages":                `{"messages":[` + connectQueuedMessage + `]}`,
		"GET /fixture-connect-settings":                             ConnectDownloadPayload,
		"GET /fixture-connect-unknown":                              "synthetic unknown destination",
		"PUT /device-service/devicemessage/message/42":              `{}`,
		"POST /upload-service/upload/activity":                      `{"detailedImportResult":{"successes":[{"messages":[{"content":"Synthetic activity accepted"}]}]}}`,
		http.MethodGet + " " + ConnectRawPath:                       `{"fixture":"connect","count":42}`,
	}
}

const connectQueuedMessage = `{"messageId":42,"messageType":"settings-change","messageStatus":"new","deviceId":42,"deviceName":"Fixture watch","deviceXmlDataType":"FIT_TYPE_41","metaData":{"fileType":"FIT","messageUrl":"https://connectapi.garmin.com/fixture-connect-settings","absolute":true,"messageName":"Fixture settings"}}`

func connectQueryMatches(req *http.Request) bool {
	switch req.URL.Path {
	case "/device-service/devicemessage/messages":
		return req.URL.RawQuery == "device_id=42"
	case "/device-service/devicemessage/message/42":
		return req.URL.RawQuery == "status=received"
	default:
		return req.URL.RawQuery == ""
	}
}

func connectFailureRequest(mode string) string {
	return map[string]string{
		"settings read failure": "GET /device-service/deviceservice/device-info/settings/42",
		"settings put failure":  "PUT /device-service/deviceservice/device-info/settings/42",
		"queue failure":         "GET /device-service/devicemessage/messages",
		"download failure":      "GET /fixture-connect-settings",
		"ack failure":           "PUT /device-service/devicemessage/message/42",
		"upload failure":        "POST /upload-service/upload/activity",
	}[mode]
}

func (f connectTransport) response(path, body string) (int, string) {
	if path == "/upload-service/upload/activity" {
		if f.mode == "duplicate upload" {
			return http.StatusConflict, "synthetic duplicate activity"
		}

		return http.StatusCreated, body
	}

	if path == "/device-service/devicemessage/messages" {
		switch f.mode {
		case "empty", "empty messages":
			return http.StatusOK, `{"messages":[]}`
		case "unknown destination":
			unknown := `{"messageId":43,"messageType":"other","deviceXmlDataType":"UNSUPPORTED","metaData":{"fileType":"UNKNOWN","messageUrl":"https://connectapi.garmin.com/fixture-connect-unknown","messageName":"Leave queued"}}`
			return http.StatusOK, `{"messages":[` + connectQueuedMessage + `,` + unknown + `]}`
		}
	}

	return http.StatusOK, body
}
