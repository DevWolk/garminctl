package testdump

import (
	"encoding/json"
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

// WorkoutSpecJSON exercises normalization, repeat groups and target mapping.
const WorkoutSpecJSON = `{"name":" Fixture workout ","sport":" RUNNING ","description":"Synthetic only","steps":[{"duration":"10m","type":" WARMUP "},{"repeat":2,"steps":[{"distance":"400m","target":"pace 4:30-4:45"},{"duration":"90s","target":"hr zone 2"}]},{"calories":20,"target":"power 200-250"}]}`

// WorkoutPayload is an opaque, synthetic queued file; it never reaches USB.
const WorkoutPayload = "synthetic queued workout"

// WorkoutRoot adds the existing NewFiles directory used by local writes.
func WorkoutRoot(tb testing.TB) string {
	tb.Helper()

	root := Root(tb)
	if err := os.MkdirAll(filepath.Join(root, "GARMIN", "NewFiles"), 0o750); err != nil {
		tb.Fatal(err)
	}

	return root
}

// WorkoutClient supplies a synthetic saved account and rejects unlisted HTTP.
func WorkoutClient(tb testing.TB, mode string) *gc.Client {
	tb.Helper()
	return workoutClient(tb, filepath.Join(tb.TempDir(), "account.json"), mode)
}

func workoutClient(tb testing.TB, store, mode string) *gc.Client {
	tb.Helper()

	session := gc.Session{Email: "workout@example.invalid", OAuth1: &gc.OAuth1Token{Token: "synthetic", Domain: gc.DefaultDomain}, OAuth2: &gc.OAuth2Token{AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).Unix()}}

	data, err := json.Marshal(session)
	if err != nil {
		tb.Fatal(err)
	}

	if err := os.WriteFile(store, data, 0o600); err != nil {
		tb.Fatal(err)
	}

	client := gc.New(store)
	if err := client.StoreError(); err != nil {
		tb.Fatal(err)
	}

	client.HTTP.Transport = workoutTransport{mode: mode}

	return client
}

type workoutTransport struct{ mode string }

func (f workoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := http.StatusOK, `{}`

	switch req.Method + " " + req.URL.Path {
	case "GET /workout-service/workouts":
		body = `[{"workoutId":42,"workoutName":"Fixture workout","sportType":{"sportTypeKey":"running"},"estimatedDurationInSecs":123.6,"updatedDate":"2000-01-02T12:00:00"},{"workoutId":43}]`
		if f.mode == "empty" {
			body = `[]`
		}
	case "POST /workout-service/workout":
		body = `{"workoutId":42,"estimatedDurationInSecs":123.6}`
		if f.mode == "zero" {
			body = `{"workoutId":0}`
		}
	case "DELETE /workout-service/workout/42":
	case "POST /device-service/devicemessage/messages":
		if f.mode == "queue failure" {
			status, body = http.StatusConflict, `fixture queue rejected`
		}
	case "GET /device-service/devicemessage/messages":
		body = `{"messages":[{"messageId":99,"messageType":"settings-change","metaData":{"fileType":"FIT","messageUrl":"https://connectapi.garmin.com/forbidden","messageName":"Leave queued"}},{"messageId":42,"messageType":"workouts","metaData":{"fileType":"FIT","messageUrl":"https://connectapi.garmin.com/fixture-workout","messageName":"Fixture workout"}}]}`
	case "GET /fixture-workout":
		body = WorkoutPayload
	case "PUT /device-service/devicemessage/message/42":
		if req.URL.Query().Get("status") != "received" {
			return nil, fmt.Errorf("unexpected ack query: %s", req.URL)
		}

		if f.mode == "ack failure" {
			status, body = http.StatusConflict, `fixture ack rejected`
		}
	default:
		return nil, fmt.Errorf("unexpected workout fixture request: %s %s", req.Method, req.URL)
	}

	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// WorkoutCLIEnvironment isolates CLI account paths and its default transport.
// Tests using this helper must not run in parallel.
func WorkoutCLIEnvironment(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())

	store := gc.DefaultStorePath()
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}

	client := workoutClient(t, store, mode)
	previous := http.DefaultTransport
	http.DefaultTransport = client.HTTP.Transport

	t.Cleanup(func() { http.DefaultTransport = previous })
}
