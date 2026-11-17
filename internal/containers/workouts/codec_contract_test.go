package workouts

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestWorkoutRejectsInvalidNumbersBeforeNarrowing(t *testing.T) {
	t.Parallel()

	cases := []StepSpec{
		{Duration: "5000000s"},
		{Duration: "-1:00"},
		{Duration: "NaN:00"},
		{Duration: "Inf:00"},
		{Duration: "-90s"},
		{Duration: "1e309s"},
		{Duration: "invalid10s"},
		{Distance: "50000000m"},
		{Target: "speed invalid-12"},
		{Target: "hr 120-invalid"},
		{Target: "speed 1e309-2e309"},
		{Target: "hr NaN-120"},
		{Target: "hr -1-120"},
		{Target: "pace Inf-4:00"},
		{Target: "pace NaN-4:00"},
		{Target: "cadence 5000000000-5000000001"},
		{Target: "power 4294966296-4294966297"},
		{Repeat: -1, Lap: true},
	}
	for i, step := range cases {
		w := &WorkoutSpec{Name: "invalid numeric input", Steps: []StepSpec{step}}
		if data, err := w.FIT(1, time.Unix(1700000000, 0)); err == nil || data != nil {
			t.Fatalf("case %d produced a FIT: bytes=%d error=%v", i, len(data), err)
		}

		if doc, err := w.ConnectJSON(); err == nil || doc != nil {
			t.Fatalf("case %d produced Connect JSON: %+v, %v", i, doc, err)
		}
	}
}

func TestWorkoutRetainsProtocolUnitTruncation(t *testing.T) {
	t.Parallel()

	for _, step := range []StepSpec{
		{Duration: "10min"},
		{Duration: "90sec"},
		{Duration: "1h 5min"},
		{Duration: "4294967.295s"},
		{Distance: "42949672.95m"},
		{Duration: "0.0009s"},
		{Target: "cadence 4294967295-4294967295"},
		{Target: "power 4294966295-4294966295"},
		{Target: "hr 4294967195-4294967195"},
	} {
		w := &WorkoutSpec{Name: "boundary", Steps: []StepSpec{step}}
		if _, err := w.FIT(1, time.Unix(1700000000, 0)); err != nil {
			t.Fatalf("valid protocol boundary %+v: %v", step, err)
		}
	}
}

func TestWorkoutRejectsRepeatAndEstimateOverflow(t *testing.T) {
	t.Parallel()

	if strconv.IntSize < 64 {
		t.Skip("repeat count above uint32 cannot be represented by the host int")
	}

	largeRepeat := int64(math.MaxUint32) + 1

	w := &WorkoutSpec{Name: "repeat overflow", Steps: []StepSpec{{Repeat: int(largeRepeat), Steps: []StepSpec{{Lap: true}}}}}
	if err := w.Validate(); err == nil {
		t.Fatal("repeat count overflow accepted")
	}

	largeRepeat--

	steps := make([]StepSpec, 512)
	for i := range steps {
		steps[i].Duration = "4294967s"
	}

	w = &WorkoutSpec{Name: "estimate overflow", Steps: []StepSpec{{Repeat: int(largeRepeat), Steps: steps}}}
	if doc, err := w.ConnectJSON(); err == nil || doc != nil {
		t.Fatalf("overflowing estimate produced Connect JSON: %+v, %v", doc, err)
	}
}

func TestWorkoutCodecContracts(t *testing.T) {
	t.Parallel()

	var spec WorkoutSpec
	if err := json.Unmarshal([]byte(testdump.WorkoutSpecJSON), &spec); err != nil {
		t.Fatal(err)
	}

	doc, err := spec.ConnectJSON()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("connect", func(t *testing.T) {
		t.Parallel()
		testdump.CheckJSON(t, doc, "26cd7b8b2dedfda928a1aa7c4d8bff051b1f3fb689466c1ad5a6ed4140c7d96d")
	})
	t.Run("normalized spec", func(t *testing.T) {
		t.Parallel()
		testdump.CheckJSON(t, spec, "8e8c57e7fb1524744eae8a2a01c25b4c3518b94c33b5bc77dd536b510c5f263c")
	})

	data, err := spec.FIT(0xfedcba9876543210, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("FIT", func(t *testing.T) {
		t.Parallel()
		testdump.CheckDigest(t, data, "4cab94c37c951368f404157ef620824f265ee3a40729c40ec71885fb0758db02")
	})
}

func TestGeneratedWorkoutFITPreservesExistingBytes(t *testing.T) {
	t.Parallel()

	// Captured from the parent implementation before changing error handling.
	data, err := sampleSpec().FIT(1234567890, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}

	testdump.CheckDigest(t, data, "2397e036ecd217ea7ef2bb4bff3939a23268226684ef79e10af7f2869cd3c480")

	if _, err := fit.Decode(data); err != nil {
		t.Fatal(err)
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

func TestGeneratedWorkoutFITUsesLow32SerialBits(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0)

	data, err := sampleSpec().FIT(0xfedcba9876543210, now)
	if err != nil {
		t.Fatal(err)
	}

	low, err := sampleSpec().FIT(0x76543210, now)
	if err != nil || !bytes.Equal(data, low) {
		t.Fatalf("workout serial boundary changed: %v", err)
	}
}
