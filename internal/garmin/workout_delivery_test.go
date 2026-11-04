package garmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type workoutWriteDevice struct {
	device.Device
	fail bool
}

func (d workoutWriteDevice) Write(ctx context.Context, path string, data []byte) error {
	if d.fail {
		return errors.New("fixture write failed")
	}

	return d.Device.Write(ctx, path, data)
}

func TestWorkoutDeliveryRetainsQueueAndWriteBeforeAck(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, mode string
		fail, job  bool
	}{
		{name: "bounded"},
		{name: "managed job", job: true},
		{name: "failed write", fail: true},
		{name: "failed ack", mode: "ack failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkWorkoutDelivery(t, test.mode, test.fail, test.job)
		})
	}
}

func checkWorkoutDelivery(t *testing.T, mode string, fail, job bool) {
	t.Helper()
	root := testdump.WorkoutRoot(t)
	client := testdump.WorkoutClient(t, mode)
	transport := client.HTTP.Transport
	acks, queued, downloads, opens := 0, 0, 0, 0
	client.HTTP.Transport = syncTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost && req.URL.Path == deviceMessagesPath {
			queued++

			checkWorkoutQueueBody(t, req)
		}

		if req.URL.Path == "/fixture-workout" {
			downloads++
		}

		if req.Method == http.MethodPut {
			acks++

			checkDeliveredWorkout(t, root)
		}

		return transport.RoundTrip(req)
	})
	watch := NewLazy(func(context.Context) (device.Device, error) {
		opens++
		fs, err := device.NewDirFS(root)

		return workoutWriteDevice{Device: fs, fail: fail}, err
	})
	t.Cleanup(watch.Drop)

	lease, err := watch.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	cloud := NewCloud(lease.Service(), client)
	if job {
		cloud = NewJobCloud(watch, lease.Epoch(), client)
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Cleanup(func() { _ = lease.Release() })
	}

	pulled, err := cloud.SendWorkout(t.Context(), 42, "Fixture workout")
	if err != nil || len(pulled) != 1 || pulled[0].MessageID != 42 || queued != 1 || downloads != 1 || opens != 1 {
		t.Fatalf("pulled=%+v error=%v queue=%d downloads=%d opens=%d", pulled, err, queued, downloads, opens)
	}

	checkWorkoutDeliveryResult(t, pulled, acks, fail, mode)
}

func checkWorkoutDeliveryResult(t *testing.T, pulled []PulledFile, acks int, fail bool, mode string) {
	t.Helper()

	if fail {
		if acks != 0 || pulled[0].Path != "" || !strings.Contains(pulled[0].Error, "fixture write failed") {
			t.Fatalf("failed write: acks=%d pulled=%+v", acks, pulled)
		}
	} else if acks != 1 || pulled[0].Bytes != len(testdump.WorkoutPayload) || (pulled[0].Error != "") != (mode == "ack failure") {
		t.Fatalf("delivery: acks=%d pulled=%+v", acks, pulled)
	}
}

func checkDeliveredWorkout(t *testing.T, root string) {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(root, "GARMIN", "NewFiles", "*.FIT"))
	if err != nil || len(files) != 1 {
		t.Fatalf("ack before single write: files=%v error=%v", files, err)
	}

	data, err := os.ReadFile(files[0])
	if err != nil || string(data) != testdump.WorkoutPayload {
		t.Fatalf("ack before complete write: data=%q error=%v", data, err)
	}
}

func checkWorkoutQueueBody(t *testing.T, req *http.Request) {
	t.Helper()

	var rows []map[string]any
	if err := json.NewDecoder(req.Body).Decode(&rows); err != nil || len(rows) != 1 {
		t.Fatalf("queue payload=%v error=%v", rows, err)
	}

	want := map[string]any{"messageUrl": "workout-service/workout/FIT/42", "messageType": "workouts", "groupName": nil, "messageName": "Fixture workout", "priority": float64(1), "fileType": "FIT", "metaDataId": float64(42)}

	if id, ok := rows[0]["deviceId"].(float64); !ok || id <= 0 {
		t.Fatal("queue payload has no watch unit ID")
	}

	delete(rows[0], "deviceId")

	if !reflect.DeepEqual(rows[0], want) {
		t.Fatalf("queue payload=%v, want=%v", rows[0], want)
	}
}

func TestWorkoutDeliveryForBorrowsCanonicalFacade(t *testing.T) {
	t.Parallel()
	root := testdump.WorkoutRoot(t)
	opens := 0
	watch := NewLazy(func(context.Context) (device.Device, error) { opens++; return device.NewDirFS(root) })
	t.Cleanup(watch.Drop)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	lease, err := watch.Manager().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = lease.Release() }()

	account := testdump.WorkoutClient(t, "")

	provider, ok := watch.WorkoutDeliveryFor(lease.Session(), account).(workoutDelivery)
	if !ok || provider.cloud.svc != watch.Current() || provider.cloud.gc != account {
		t.Fatal("queue provider created separate facade/account")
	}

	cache := watch.AppSchemaCacheFor(lease.Session())
	if cache != &provider.cloud.svc.schemas {
		t.Fatal("queue provider bypassed canonical schema cache")
	}

	if dx, err := provider.DeviceXML(ctx); err != nil || dx.UnitID == 0 {
		t.Fatalf("borrowed metadata=%+v error=%v", dx, err)
	}

	if opens != 1 {
		t.Fatalf("queue provider reopened watch: %d", opens)
	}
}
