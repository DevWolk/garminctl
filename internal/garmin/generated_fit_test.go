package garmin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestGeneratedFITPreservesExistingBytes(t *testing.T) {
	t.Parallel()

	index := 4
	changes := []ResolvedChange{
		{MesgNum: 2, FieldNum: 58, Raw: 1500},
		{Index: 5, ArrayIndex: &index, MesgNum: 2, FieldNum: 9, Raw: -123},
	}
	now := time.Unix(1700000000, 0)

	// Captured from the parent implementation before changing error handling.
	settings, err := BuildChangeFile(changes, 1234567890, 1705, now)
	if err != nil {
		t.Fatal(err)
	}

	assertGeneratedFIT(t, settings, "d81bf08eea4d9d3d63c4b241085a939ecd3e0f829d454e99113ee861f79ca8f7")

	workout, err := sampleSpec().FIT(1234567890, now)
	if err != nil {
		t.Fatal(err)
	}

	assertGeneratedFIT(t, workout, "5a39997cfb75ce656e78a2ea207c35ab8bb74407a572ca84c7fe2edc68a86d9f")
}

func TestBuildChangeFilePropagatesEncodingFailure(t *testing.T) {
	t.Parallel()

	arrayIndex := math.MaxUint8 + 1
	for _, change := range []ResolvedChange{
		{Index: math.MaxUint16 + 1, MesgNum: 2, FieldNum: 58, Raw: 1500},
		{ArrayIndex: &arrayIndex, MesgNum: 2, FieldNum: 58, Raw: 1500},
	} {
		data, err := BuildChangeFile([]ResolvedChange{change}, 1, 1, time.Unix(1700000000, 0))
		if err == nil || data != nil {
			t.Fatalf("invalid field produced a partial FIT: %x, %v", data, err)
		}
	}
}

type fitWriteDevice struct {
	device.Device
	writes int
}

func (d *fitWriteDevice) Write(ctx context.Context, path string, data []byte) error {
	d.writes++
	return d.Device.Write(ctx, path, data)
}

func TestApplyChangesDoesNotWriteAfterEncodingFailure(t *testing.T) {
	t.Parallel()

	fs, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	d := &fitWriteDevice{Device: fs}
	change := Change{Mesg: "2", Field: "58", Index: math.MaxUint16 + 1, Value: 1500}

	result, err := New(d).ApplyChanges(t.Context(), []Change{change})
	if err == nil || result != nil || d.writes != 0 {
		t.Fatalf("invalid settings staged: result=%+v error=%v writes=%d", result, err, d.writes)
	}
}

func TestWorkoutFITRejectsUnencodableStepCount(t *testing.T) {
	t.Parallel()

	w := &WorkoutSpec{Name: "too many steps", Sport: "running", Steps: make([]StepSpec, math.MaxUint16+1)}

	data, err := w.FIT(1, time.Unix(1700000000, 0))
	if err == nil || data != nil {
		t.Fatalf("invalid workout produced a partial FIT: bytes=%d error=%v", len(data), err)
	}
}

func assertGeneratedFIT(t *testing.T, data []byte, hash string) {
	t.Helper()

	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != hash {
		t.Fatalf("generated FIT changed: SHA-256 = %s, want %s", got, hash)
	}

	if _, err := fit.Decode(data); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedFITUsesLow32SerialBits(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0)

	const unit = uint64(0xfedcba9876543210)

	settings, err := BuildChangeFile(nil, unit, 1705, now)
	if err != nil {
		t.Fatal(err)
	}

	lowSettings, err := BuildChangeFile(nil, 0x76543210, 1705, now)
	if err != nil || !bytes.Equal(settings, lowSettings) {
		t.Fatalf("settings serial boundary changed: %v", err)
	}

	workout, err := sampleSpec().FIT(unit, now)
	if err != nil {
		t.Fatal(err)
	}

	lowWorkout, err := sampleSpec().FIT(0x76543210, now)
	if err != nil || !bytes.Equal(workout, lowWorkout) {
		t.Fatalf("workout serial boundary changed: %v", err)
	}

	decoded, err := fit.Decode(settings)
	if err != nil {
		t.Fatal(err)
	}

	if got := decoded.MessagesByNum(0)[0].Value(3); got != uint64(0x76543210) {
		t.Fatalf("decoded serial = %#v", got)
	}
}
