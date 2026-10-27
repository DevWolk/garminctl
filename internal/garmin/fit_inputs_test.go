package garmin

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestSettingsRejectInvalidValuesBeforeWriting(t *testing.T) {
	t.Parallel()

	negativeIndex, largeIndex := -1, math.MaxUint8+1
	cases := []Change{
		{Mesg: "2", Field: "58", Index: -1, Value: 1},
		{Mesg: "2", Field: "58", ArrayIndex: &negativeIndex, Value: 1},
		{Mesg: "2", Field: "58", ArrayIndex: &largeIndex, Value: 1},
		{Mesg: "2", Field: "58", Value: math.NaN()},
		{Mesg: "2", Field: "58", Value: math.Inf(1)},
		{Mesg: "2", Field: "58", Value: math.Inf(-1)},
		{Mesg: "2", Field: "58", Value: float64(0x1p63)},
		{Mesg: "65000", Field: "1", Value: 1.5},
	}

	fs, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	d := &fitWriteDevice{Device: fs}

	svc := New(d)
	for _, change := range cases {
		result, err := svc.ApplyChanges(t.Context(), []Change{change})
		if err == nil || result != nil || d.writes != 0 {
			t.Fatalf("invalid setting %+v staged: %+v, %v, writes=%d", change, result, err, d.writes)
		}
	}
}

func TestSettingsBuildChecksRawBeforeNarrowing(t *testing.T) {
	t.Parallel()

	for _, raw := range []int64{math.MinInt32 - 1, math.MaxUint32 + 1, math.MaxInt64} {
		data, err := BuildChangeFile([]ResolvedChange{{MesgNum: 2, FieldNum: 58, Raw: raw}}, 1, 1, time.Unix(1700000000, 0))
		if err == nil || data != nil {
			t.Fatalf("raw %d produced partial file: %d bytes, %v", raw, len(data), err)
		}
	}

	for _, spec := range []string{"2[-1].58=1", "2[65536].58=1", "2.58[-1]=1", "2.58[256]=1"} {
		if _, err := ParseChangeSpec(spec); err == nil {
			t.Fatalf("invalid index accepted: %s", spec)
		}
	}
}

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

func TestWriteWorkoutRejectsOverflowBeforeDeviceWrite(t *testing.T) {
	t.Parallel()

	fs, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	d := &fitWriteDevice{Device: fs}
	w := &WorkoutSpec{Name: "overflow", Steps: []StepSpec{{Duration: "5000000s"}}}

	path, err := New(d).WriteWorkoutToWatch(t.Context(), w)
	if err == nil || path != "" || d.writes != 0 {
		t.Fatalf("overflow workout staged: path=%q error=%v writes=%d", path, err, d.writes)
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
