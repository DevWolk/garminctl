package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/containers/workouts"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

const (
	deliveryMessagesPath = "/device-service/devicemessage/messages"
	deliveryProfilePath  = "/userprofile-service/socialProfile"
	ackFailureMode       = "ack failure"
)

type deliveryTransport func(*http.Request) (*http.Response, error)

func (f deliveryTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type deliveryWriteDevice struct {
	device.Device
	fail bool
}

func (d deliveryWriteDevice) Write(ctx context.Context, path string, data []byte) error {
	if d.fail {
		return errors.New("fixture write failed")
	}

	return d.Device.Write(ctx, path, data)
}

func TestHealthAccessBorrowsLeasedSessionAndKeepsIdentityPerCaller(t *testing.T) {
	t.Parallel()

	root := testdump.HealthRoot(t)

	var opens atomic.Int32

	runtime := NewRuntime(t.Context(), func(context.Context) (device.Device, error) {
		opens.Add(1)
		return device.NewDirFS(root)
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	lease, err := runtime.Manager().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = lease.Release() }()

	client := testdump.HealthClient(t, "")
	delegate := client.HTTP.Transport

	var profiles atomic.Int32

	client.HTTP.Transport = deliveryTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == deliveryProfilePath {
			profiles.Add(1)
		}

		return delegate.RoundTrip(req)
	})
	cache := runtime.AppSchemaCacheFor(lease.Session())
	accessFor := HealthAccess(runtime, client)

	access := accessFor(lease.Session())
	if access.Client != client {
		t.Fatal("health access replaced the shared account")
	}

	for range 2 {
		if name, err := access.DisplayName(ctx); err != nil || name != "fixture-health" {
			t.Fatalf("identity=%q error=%v", name, err)
		}
	}

	if profiles.Load() != 1 {
		t.Fatalf("one caller fetched %d profiles", profiles.Load())
	}

	if _, err := accessFor(lease.Session()).DisplayName(ctx); err != nil || profiles.Load() != 2 {
		t.Fatalf("next caller reused the previous caller's profile: calls=%d error=%v", profiles.Load(), err)
	}

	if dx, err := access.DeviceXML(ctx); err != nil || dx.UnitID == 0 {
		t.Fatalf("scoped metadata=%+v error=%v", dx, err)
	}

	if opens.Load() != 1 || runtime.AppSchemaCacheFor(lease.Session()) != cache {
		t.Fatalf("health access reopened the watch or replaced its schema cache: opens=%d", opens.Load())
	}
}

func TestWorkoutDeliveryBorrowsLeasedSessionAndSharedAccount(t *testing.T) {
	t.Parallel()

	root := testdump.WorkoutRoot(t)

	var opens atomic.Int32

	runtime := NewRuntime(t.Context(), func(context.Context) (device.Device, error) {
		opens.Add(1)
		return device.NewDirFS(root)
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	lease, err := runtime.Manager().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = lease.Release() }()

	account := testdump.WorkoutClient(t, "")
	cache := runtime.AppSchemaCacheFor(lease.Session())

	queue, ok := WorkoutDelivery(runtime, account)(lease.Session()).(workoutQueue)
	if !ok || queue.cloud.Client() != account {
		t.Fatal("workout delivery created a separate account")
	}

	if dx, err := queue.DeviceXML(ctx); err != nil || dx.UnitID == 0 {
		t.Fatalf("borrowed metadata=%+v error=%v", dx, err)
	}

	if opens.Load() != 1 || runtime.AppSchemaCacheFor(lease.Session()) != cache {
		t.Fatalf("workout delivery reopened the watch or replaced its schema cache: opens=%d", opens.Load())
	}
}

func TestWorkoutDeliveryQueuesAndWritesBeforeAck(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, mode string
		fail       bool
	}{
		{name: "delivered"},
		{name: "failed write", fail: true},
		{name: "failed ack", mode: ackFailureMode},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkWorkoutDelivery(t, test.mode, test.fail)
		})
	}
}

func checkWorkoutDelivery(t *testing.T, mode string, fail bool) {
	t.Helper()

	root := testdump.WorkoutRoot(t)
	client := testdump.WorkoutClient(t, mode)
	delegate := client.HTTP.Transport

	var acks, queued, downloads, opens atomic.Int32

	client.HTTP.Transport = deliveryTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost && req.URL.Path == deliveryMessagesPath {
			queued.Add(1)
			checkWorkoutQueueBody(t, req)
		}

		if req.URL.Path == "/fixture-workout" {
			downloads.Add(1)
		}

		if req.Method == http.MethodPut {
			acks.Add(1)
			checkDeliveredWorkout(t, root)
		}

		return delegate.RoundTrip(req)
	})
	runtime := NewRuntime(t.Context(), func(context.Context) (device.Device, error) {
		opens.Add(1)

		fs, err := device.NewDirFS(root)

		return deliveryWriteDevice{Device: fs, fail: fail}, err
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	lease, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = lease.Release() }()

	send := workouts.SendAction{Client: client, Queue: WorkoutDelivery(runtime, client)(lease.Session())}

	pulled, err := send.Run(t.Context(), 42, "Fixture workout")
	if err != nil || len(pulled) != 1 || pulled[0].MessageID != 42 || queued.Load() != 1 || downloads.Load() != 1 || opens.Load() != 1 {
		t.Fatalf("pulled=%+v error=%v queue=%d downloads=%d opens=%d", pulled, err, queued.Load(), downloads.Load(), opens.Load())
	}

	checkWorkoutDeliveryResult(t, pulled, int(acks.Load()), fail, mode)
}

func checkWorkoutDeliveryResult(t *testing.T, pulled []connect.PulledFile, acks int, fail bool, mode string) {
	t.Helper()

	if fail {
		if acks != 0 || pulled[0].Path != "" || !strings.Contains(pulled[0].Error, "fixture write failed") {
			t.Fatalf("failed write: acks=%d pulled=%+v", acks, pulled)
		}

		return
	}

	if acks != 1 || pulled[0].Bytes != len(testdump.WorkoutPayload) || (pulled[0].Error != "") != (mode == ackFailureMode) {
		t.Fatalf("delivery: acks=%d pulled=%+v", acks, pulled)
	}
}

// checkDeliveredWorkout runs inside the ack request, so it reports with
// Errorf instead of stopping the HTTP client's caller.
func checkDeliveredWorkout(t *testing.T, root string) {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(root, "GARMIN", "NewFiles", "*.FIT"))
	if err != nil || len(files) != 1 {
		t.Errorf("ack before single write: files=%v error=%v", files, err)
		return
	}

	data, err := os.ReadFile(files[0])
	if err != nil || string(data) != testdump.WorkoutPayload {
		t.Errorf("ack before complete write: data=%q error=%v", data, err)
	}
}

func checkWorkoutQueueBody(t *testing.T, req *http.Request) {
	t.Helper()

	var rows []map[string]any
	if err := json.NewDecoder(req.Body).Decode(&rows); err != nil || len(rows) != 1 {
		t.Errorf("queue payload=%v error=%v", rows, err)
		return
	}

	if id, ok := rows[0]["deviceId"].(float64); !ok || id <= 0 {
		t.Error("queue payload has no watch unit ID")
	}

	delete(rows[0], "deviceId")

	want := map[string]any{"messageUrl": "workout-service/workout/FIT/42", "messageType": "workouts", "groupName": nil, "messageName": "Fixture workout", "priority": float64(1), "fileType": "FIT", "metaDataId": float64(42)}
	if !reflect.DeepEqual(rows[0], want) {
		t.Errorf("queue payload=%v, want=%v", rows[0], want)
	}
}
