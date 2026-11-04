package garmin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/fit"
)

func sampleSpec() *WorkoutSpec {
	var w WorkoutSpec

	_ = json.Unmarshal([]byte(`{"name":"4x400 test","sport":"running","steps":[
	  {"type":"warmup","duration":"10:00"},
	  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},{"type":"recovery","duration":"90s","target":"hr zone 2"}]},
	  {"type":"cooldown","duration":"5m","target":"hr 110-130"}]}`), &w)

	return &w
}

// asMaps, asMap, asFloat and asIntT read a value out of the rendered JSON
// document, failing the test instead of panicking on a wrong type.
func asMaps(t *testing.T, v any) []map[string]any {
	t.Helper()

	m, ok := v.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", v)
	}

	return m
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()

	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}

	return m
}

func asFloat(t *testing.T, v any) float64 {
	t.Helper()

	f, ok := v.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", v)
	}

	return f
}

func asIntT(t *testing.T, v any) int {
	t.Helper()

	i, ok := v.(int)
	if !ok {
		t.Fatalf("expected int, got %T", v)
	}

	return i
}

func TestWorkoutConnectJSON(t *testing.T) {
	t.Parallel()

	doc, err := sampleSpec().ConnectJSON()
	if err != nil {
		t.Fatal(err)
	}

	segs := asMaps(t, doc["workoutSegments"])
	steps := asMaps(t, segs[0]["workoutSteps"])

	if len(steps) != 3 || steps[1]["type"] != "RepeatGroupDTO" || steps[1]["numberOfIterations"] != 4 {
		t.Fatalf("unexpected steps: %+v", steps)
	}

	inner := asMaps(t, steps[1]["workoutSteps"])
	if asFloat(t, inner[0]["endConditionValue"]) != 400 || asMap(t, inner[0]["targetType"])["workoutTargetTypeKey"] != "pace.zone" {
		t.Fatalf("interval step wrong: %+v", inner[0])
	}

	lo, hi := asFloat(t, inner[0]["targetValueOne"]), asFloat(t, inner[0]["targetValueTwo"])
	if lo > hi || lo < 3.4 || hi > 3.8 { // 4:45 → 3.51 m/s, 4:30 → 3.70 m/s
		t.Fatalf("pace range wrong: %v-%v", lo, hi)
	}

	if asFloat(t, steps[2]["targetValueOne"]) != 110 || asFloat(t, steps[2]["endConditionValue"]) != 300 {
		t.Fatalf("cooldown wrong: %+v", steps[2])
	}

	if asIntT(t, doc["estimatedDurationInSecs"]) <= 0 {
		t.Fatal("no estimate")
	}
}

func TestWorkoutFITRoundTrip(t *testing.T) {
	t.Parallel()

	data, err := sampleSpec().FIT(1234567890, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}

	f, err := fit.Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	if n := len(f.MessagesByNum(27)); n != 5 { // warmup, interval, recovery, repeat, cooldown
		t.Fatalf("expected 5 workout steps, got %d", n)
	}

	p := fit.MustProfile()

	wk := p.DescribeMessage(f.MessagesByNum(26)[0])
	if fieldStr(&wk, "wktName") != "4x400 test" || int(fieldNum(&wk, "numValidSteps")) != 5 {
		t.Fatalf("workout header wrong: %+v", wk.Fields)
	}

	rep := p.DescribeMessage(f.MessagesByNum(27)[3])
	if int(fieldNum(&rep, "durationValue")) != 1 || int(fieldNum(&rep, "targetValue")) != 4 {
		t.Fatalf("repeat step wrong: %+v", rep.Fields)
	}
}

func TestFitName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "short ascii", input: "4x400 test", expected: "4x400 test"},
		{name: "long ascii is cut to 15 bytes", input: "a very long workout name", expected: "a very long wor"},
		{name: "multibyte cut on a character boundary", input: "ääääääääää", expected: "äääääää"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := fitName(tt.input); got != tt.expected {
				t.Fatalf("fitName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseHelpers(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]float64{"10:00": 600, "1:30:00": 5400, "90s": 90, "10m": 600, "1h5m": 3900, "12": 720} {
		if got, err := parseDurationSec(in); err != nil || got != want {
			t.Fatalf("%s -> %v %v, want %v", in, got, err, want)
		}
	}

	for in, want := range map[string]float64{"400m": 400, "2km": 2000, "2.5k": 2500, "1mi": 1609.344} {
		if got, err := parseDistanceM(in); err != nil || got != want {
			t.Fatalf("%s -> %v %v, want %v", in, got, err, want)
		}
	}

	if tg, err := parseTarget("speed 10-12"); err != nil || tg.kind != targetSpeed || tg.low < 2.7 || tg.hi > 3.4 {
		t.Fatalf("speed target: %+v %v", tg, err)
	}

	if tg, err := parseTarget("hr zone 3"); err != nil || tg.kind != targetHRZone || tg.zone != 3 {
		t.Fatalf("zone target: %+v %v", tg, err)
	}

	if _, err := parseTarget("hr 160"); err == nil {
		t.Fatal("expected an error for a target without a range")
	}
}

func TestValidateRejectsCalorieOverflow(t *testing.T) {
	t.Parallel()

	w := &WorkoutSpec{Name: "x", Steps: []StepSpec{{Calories: maxStepCalories + 1}}}
	if err := w.Validate(); err == nil {
		t.Fatal("expected an error for calories above the limit")
	}
}

func TestTitleCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "lower word", input: "music", expected: "Music"},
		{name: "upper word", input: "PODCASTS", expected: "Podcasts"},
		{name: "underscores become spaces", input: "trail_running", expected: "Trail Running"},
		{name: "empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := titleCase(tt.input); got != tt.expected {
				t.Fatalf("titleCase(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
