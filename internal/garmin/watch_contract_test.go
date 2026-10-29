package garmin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type summaryFaultDevice struct {
	device.Device
	metadata, registry, pending, storage error
	zeroStorage                          bool
	steps                                []string
}

func (d *summaryFaultDevice) Read(ctx context.Context, p string) ([]byte, error) {
	var err error

	switch p {
	case PathDeviceXML:
		d.steps = append(d.steps, "metadata")
		err = d.metadata
	case PathAppsConfig:
		d.steps = append(d.steps, "registry")
		err = d.registry
	}

	if err != nil {
		return nil, err
	}

	return d.Device.Read(ctx, p)
}

func (d *summaryFaultDevice) List(ctx context.Context, p string) ([]device.Entry, error) {
	d.steps = append(d.steps, "pending")
	if d.pending != nil {
		return nil, d.pending
	}

	return d.Device.List(ctx, p)
}

func (d *summaryFaultDevice) Storage(ctx context.Context) (device.Storage, error) {
	d.steps = append(d.steps, "storage")
	if d.storage != nil || d.zeroStorage {
		return device.Storage{}, d.storage
	}

	reporter, ok := d.Device.(device.StorageReporter)
	if !ok {
		return device.Storage{}, errors.New("fixture storage capability missing")
	}

	return reporter.Storage(ctx)
}

func TestSummaryOptionalFailuresAndReadOrder(t *testing.T) {
	t.Parallel()

	failure := errors.New("synthetic optional failure")
	for _, test := range []struct {
		name                                 string
		metadata, registry, pending, storage error
		zeroStorage                          bool
	}{
		{name: "complete"},
		{name: "registry unavailable", registry: failure},
		{name: "pending unavailable", pending: failure},
		{name: "storage unavailable", storage: failure},
		{name: "zero capacity", zeroStorage: true},
		{name: "metadata required", metadata: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			base, _ := testdump.Watch(t)
			dev := &summaryFaultDevice{Device: base, metadata: test.metadata, registry: test.registry, pending: test.pending, storage: test.storage, zeroStorage: test.zeroStorage}
			svc := New(dev)

			t.Cleanup(func() { _ = svc.Close() })

			sum, err := svc.Summary(t.Context())
			if test.metadata != nil {
				if !errors.Is(err, failure) || sum != nil || !reflect.DeepEqual(dev.steps, []string{"metadata"}) {
					t.Fatalf("required metadata result=%+v error=%v steps=%v", sum, err, dev.steps)
				}

				return
			}

			if err != nil || sum == nil {
				t.Fatalf("summary=%+v error=%v", sum, err)
			}

			if !reflect.DeepEqual(dev.steps, []string{"metadata", "registry", "pending", "storage"}) {
				t.Fatalf("summary read order=%v", dev.steps)
			}

			if (sum.AppCount == 0) != (test.registry != nil) {
				t.Fatalf("registry fallback app count=%d", sum.AppCount)
			}

			wantPending := 2
			if test.pending != nil {
				wantPending = 0
			}

			if sum.NewFiles != wantPending {
				t.Fatalf("pending=%d want %d", sum.NewFiles, wantPending)
			}

			if (sum.Storage == nil) != (test.storage != nil || test.zeroStorage) {
				t.Fatalf("storage fallback=%+v", sum.Storage)
			}
		})
	}
}

func TestSummaryZeroJSONAndDecodeErrorContract(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Summary{})

	const want = `{"device":{"id":"","name":""},"model":"","partNumber":"","unitId":0,"software":"","appCount":0,"pendingNewFiles":0,"fitProfile":""}`
	if err != nil || string(data) != want {
		t.Fatalf("zero summary=%s error=%v", data, err)
	}

	dev, _ := testdump.Watch(t)
	svc := New(dev)

	t.Cleanup(func() { _ = svc.Close() })

	if out, err := svc.DecodeFIT(t.Context(), "GARMIN/sample.bin"); err == nil || out != nil || !strings.HasPrefix(err.Error(), "GARMIN/sample.bin: ") {
		t.Fatalf("decode result=%v error=%v", out, err)
	}
}
