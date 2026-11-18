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

const workoutsAPI = "/api/workouts"

func TestWorkoutsHTTPContracts(t *testing.T) { //nolint:paralleltest // cases isolate process account paths and transports.
	for _, test := range []struct { //nolint:paralleltest // cases isolate process account paths and transports.
		name, method, path, body, mode string
		status                         int
		normalize                      bool
		hash                           string
	}{
		{name: "workouts list", method: http.MethodGet, path: workoutsAPI, status: 200, hash: "5757e648bb2b44529889d8e3b240b9487db83d3f572ee0bd308a57b9c7118b82"},
		{name: "empty workouts", method: http.MethodGet, path: workoutsAPI, mode: emptyFixtureMode, status: 200, hash: "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570"},
		{name: "preview", method: http.MethodPost, path: "/api/workouts/preview", body: `{"spec":` + testdump.WorkoutSpecJSON + `}`, status: 200, hash: "17d2a575c4eddece7fe98974a0d7cd018cd4ed840f18d9a9cc06826b4a510d11"},
		{name: "invalid preview", method: http.MethodPost, path: "/api/workouts/preview", body: `{"spec":{}}`, status: 400, hash: "af00d8cc85b9a2bc18b49de20d31d98d3dc37d9bdb9f6ad6c511d1656c32d937"},
		{name: "trailing JSON", method: http.MethodPost, path: "/api/workouts/preview", body: `{} {}`, status: 400, hash: contractTrailingJSONHash},
		{name: "create", method: http.MethodPost, path: workoutsAPI, body: `{"spec":` + testdump.WorkoutSpecJSON + `}`, status: 200, hash: "955d52395dbd444594f65e836e450971f7c39b376bffd5b2da983c793aeb3aa5"},
		{name: "local overrides send", method: http.MethodPost, path: workoutsAPI, body: `{"spec":` + testdump.WorkoutSpecJSON + `,"local":true,"send":true}`, status: 200, normalize: true, hash: "6e58a7d67ad6b2f01a66568d3fa03a61d4b745bf3b1426c39cc6876347aa97ac"},
		{name: "create and send", method: http.MethodPost, path: workoutsAPI, body: `{"spec":` + testdump.WorkoutSpecJSON + `,"send":true}`, status: 200, normalize: true, hash: "8d0dcf9a6e9d7fdd89811ac49d4c8e3651c42736a997b9c56d844e268aa18574"},
		{name: "partial create", method: http.MethodPost, path: workoutsAPI, body: `{"spec":` + testdump.WorkoutSpecJSON + `,"send":true}`, mode: contractQueueFailure, status: 200, hash: "3f368951b8569048e5eec8c32ab3dfc2d8362c023144296efd7e4c22f5dbea52"},
		{name: "zero id", method: http.MethodPost, path: workoutsAPI, body: `{"spec":` + testdump.WorkoutSpecJSON + `}`, mode: "zero", status: 500, hash: "51968c6d7d517eb5fa375f67718ceb9d7c88a130dfd35de133e1959fe236b8f5"},
		{name: "send", method: http.MethodPost, path: "/api/workouts/42/send?name=Fixture", status: 200, normalize: true, hash: "da0d254c407f86f042e17f21fc28429d68a4c0a15eff37f4ae0cc8bac64285dd"},
		{name: "ack failure", method: http.MethodPost, path: "/api/workouts/42/send", mode: ackFailureMode, status: 200, normalize: true, hash: "7ae7e7f78707777b6e10bc4a80c48e8f628030fc31bb91eafb5692c0cbefaef1"},
		{name: "send failure", method: http.MethodPost, path: "/api/workouts/42/send", mode: contractQueueFailure, status: 500, hash: "ca76c7c42120061cbcdd4ed9090547982474c2efca355061cf8822c5fcf748c0"},
		{name: "bad send id", method: http.MethodPost, path: "/api/workouts/no/send", status: 400, hash: "9ed27c99b693f4627830cb243ccb7b1d1ee42d2fb1f1c2fe17bb02032e1e75ba"},
		{name: "delete workout", method: http.MethodDelete, path: "/api/workouts/42", status: 200, hash: ""},
		{name: "bad delete id", method: http.MethodDelete, path: "/api/workouts/no", status: 400, hash: "9ed27c99b693f4627830cb243ccb7b1d1ee42d2fb1f1c2fe17bb02032e1e75ba"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.WorkoutCLIEnvironment(t, test.mode)
			root := testdump.WorkoutRoot(t)
			ts := serveContractRoot(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

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

			if test.name == "delete workout" {
				if string(data) != "{\n \"ok\": true\n}\n" {
					t.Fatalf("delete response=%s", data)
				}

				return
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}
