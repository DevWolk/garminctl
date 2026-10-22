package garmin

import (
	"encoding/json"
	"testing"
	"time"

	"garminctl/internal/fit"
)

func sampleSpec() *WorkoutSpec {
	var w WorkoutSpec
	_ = json.Unmarshal([]byte(`{"name":"4x400 test","sport":"running","steps":[
	  {"type":"warmup","duration":"10:00"},
	  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},{"type":"recovery","duration":"90s","target":"hr zone 2"}]},
	  {"type":"cooldown","duration":"5m","target":"hr 110-130"}]}`), &w)
	return &w
}

func TestWorkoutConnectJSON(t *testing.T) {
	doc, err := sampleSpec().ConnectJSON()
	if err != nil {
		t.Fatal(err)
	}
	segs := doc["workoutSegments"].([]map[string]any)
	steps := segs[0]["workoutSteps"].([]map[string]any)
	if len(steps) != 3 || steps[1]["type"] != "RepeatGroupDTO" || steps[1]["numberOfIterations"] != 4 {
		t.Fatalf("unexpected steps: %+v", steps)
	}
	inner := steps[1]["workoutSteps"].([]map[string]any)
	if inner[0]["endConditionValue"].(float64) != 400 || inner[0]["targetType"].(map[string]any)["workoutTargetTypeKey"] != "pace.zone" {
		t.Fatalf("interval step wrong: %+v", inner[0])
	}
	lo, hi := inner[0]["targetValueOne"].(float64), inner[0]["targetValueTwo"].(float64)
	if lo > hi || lo < 3.4 || hi > 3.8 { // 4:45 → 3.51 m/s, 4:30 → 3.70 m/s
		t.Fatalf("pace range wrong: %v-%v", lo, hi)
	}
	if steps[2]["targetValueOne"].(float64) != 110 || steps[2]["endConditionValue"].(float64) != 300 {
		t.Fatalf("cooldown wrong: %+v", steps[2])
	}
	if doc["estimatedDurationInSecs"].(int) <= 0 {
		t.Fatal("no estimate")
	}
}

func TestWorkoutFITRoundTrip(t *testing.T) {
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

func TestParseHelpers(t *testing.T) {
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
	if tg, err := parseTarget("speed 10-12"); err != nil || tg.kind != "speed" || tg.low < 2.7 || tg.hi > 3.4 {
		t.Fatalf("speed target: %+v %v", tg, err)
	}
	if _, err := parseTarget("hr 160"); err == nil {
		t.Fatal("expected an error for a target without a range")
	}
}

func TestReadTagsID3v2(t *testing.T) {
	frame := func(id, text string) []byte {
		body := append([]byte{0}, []byte(text)...)
		b := []byte(id)
		b = append(b, byte(len(body)>>24), byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
		b = append(b, 0, 0)
		return append(b, body...)
	}
	frames := append(frame("TIT2", "Song"), frame("TPE1", "Artist/Name")...)
	frames = append(frames, frame("TALB", "Album")...)
	size := len(frames)
	hdr := []byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}
	data := append(hdr, frames...)
	tags := ReadTags(data)
	if tags.Title != "Song" || tags.Artist != "Artist/Name" || tags.Album != "Album" {
		t.Fatalf("tags: %+v", tags)
	}
	if safeName(tags.Artist, "x") != "Artist Name" {
		t.Fatalf("safeName: %q", safeName(tags.Artist, "x"))
	}
}
