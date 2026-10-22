package garmin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"garminctl/internal/fit"
)

// WorkoutSpec is the prompt-friendly description of a structured workout.
//
//	{"name":"4x400","sport":"running","steps":[
//	  {"type":"warmup","duration":"10:00"},
//	  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},{"type":"recovery","duration":"1:30"}]},
//	  {"type":"cooldown","duration":"5:00"}]}
type WorkoutSpec struct {
	Name        string     `json:"name"`
	Sport       string     `json:"sport"` // running, cycling, swimming, walking, hiking, strength, cardio, hiit, yoga, pilates, other
	Description string     `json:"description,omitempty"`
	Steps       []StepSpec `json:"steps"`
}

// StepSpec is one step or a repeat group.
type StepSpec struct {
	Type     string     `json:"type,omitempty"`     // warmup, cooldown, interval, recovery, rest, other
	Name     string     `json:"name,omitempty"`     // optional label
	Duration string     `json:"duration,omitempty"` // "10:00", "90s", "10m", "1h"
	Distance string     `json:"distance,omitempty"` // "400m", "2km", "1mi"
	Calories int        `json:"calories,omitempty"`
	Lap      bool       `json:"lap,omitempty"`    // end with the lap button
	Target   string     `json:"target,omitempty"` // "hr 140-160", "hr zone 3", "pace 4:30-4:45" (min/km), "speed 10-12" (km/h), "cadence 170-180", "power 200-250"
	Repeat   int        `json:"repeat,omitempty"` // >0: a repeat group of Steps
	Steps    []StepSpec `json:"steps,omitempty"`
}

var (
	sportIDs  = map[string][2]any{"running": {1, "running"}, "cycling": {2, "cycling"}, "other": {3, "other"}, "swimming": {4, "swimming"}, "strength": {5, "strength_training"}, "cardio": {6, "cardio_training"}, "yoga": {7, "yoga"}, "pilates": {8, "pilates"}, "hiit": {9, "hiit"}, "walking": {17, "walking"}, "hiking": {18, "hiking"}}
	stepIDs   = map[string][2]any{"warmup": {1, "warmup"}, "cooldown": {2, "cooldown"}, "interval": {3, "interval"}, "recovery": {4, "recovery"}, "rest": {5, "rest"}, "other": {7, "other"}, "main": {8, "main"}}
	fitSport  = map[string]int{"running": 1, "cycling": 2, "other": 0, "swimming": 5, "strength": 10, "cardio": 4, "yoga": 10, "pilates": 10, "hiit": 10, "walking": 11, "hiking": 17}
	fitIntens = map[string]int{"warmup": 2, "cooldown": 3, "interval": 5, "recovery": 4, "rest": 1, "other": 6, "main": 0}
	reRange   = regexp.MustCompile(`^(\S+)\s*[-–]\s*(\S+)$`)
)

func parseDurationSec(s string) (float64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		total := 0.0
		for _, p := range parts {
			v, err := strconv.ParseFloat(p, 64)
			if err != nil {
				return 0, fmt.Errorf("bad duration %q", s)
			}
			total = total*60 + v
		}
		return total, nil
	}
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(h|m|min|s|sec)?`)
	total := 0.0
	matched := false
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		switch m[2] {
		case "h":
			total += v * 3600
		case "m", "min":
			total += v * 60
		case "s", "sec", "":
			if m[2] == "" && !strings.ContainsAny(s, "hms") {
				total += v * 60 // a bare number means minutes
			} else {
				total += v
			}
		}
		matched = true
	}
	if !matched {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	return total, nil
}

func parseDistanceM(s string) (float64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	re := regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(km|k|m|mi|miles?|yd)?$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("bad distance %q", s)
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	switch m[2] {
	case "km", "k":
		return v * 1000, nil
	case "mi", "mile", "miles":
		return v * 1609.344, nil
	case "yd":
		return v * 0.9144, nil
	}
	return v, nil
}

// target is a parsed target.
type target struct {
	kind    string  // none, hr, hrzone, pace, speed, cadence, power
	low, hi float64 // hr bpm, speed m/s, rpm, watts
	zone    int
}

func parsePaceSec(s string) (float64, error) {
	if strings.Contains(s, ":") {
		return parseDurationSec(s)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return v * 60, nil
}

func parseTarget(s string) (target, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "none" || s == "no target" {
		return target{kind: "none"}, nil
	}
	fields := strings.Fields(s)
	kind := fields[0]
	rest := strings.TrimSpace(strings.TrimPrefix(s, kind))
	if (kind == "hr" || kind == "heart" || kind == "zone") && strings.HasPrefix(rest, "zone") || kind == "zone" {
		z, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(rest, "zone"), "rate zone")))
		if err != nil {
			z, err = strconv.Atoi(strings.TrimSpace(rest))
		}
		if err != nil || z < 1 || z > 5 {
			return target{}, fmt.Errorf("bad heart-rate zone in %q", s)
		}
		return target{kind: "hrzone", zone: z}, nil
	}
	m := reRange.FindStringSubmatch(rest)
	if m == nil {
		return target{}, fmt.Errorf("target %q needs a range like 140-160", s)
	}
	t := target{}
	switch kind {
	case "hr", "heart", "heartrate", "bpm":
		t.kind = "hr"
		t.low, _ = strconv.ParseFloat(m[1], 64)
		t.hi, _ = strconv.ParseFloat(m[2], 64)
	case "pace":
		t.kind = "pace"
		a, err1 := parsePaceSec(m[1])
		b, err2 := parsePaceSec(m[2])
		if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
			return target{}, fmt.Errorf("bad pace in %q (use min:sec per km)", s)
		}
		t.low, t.hi = 1000/math.Max(a, b), 1000/math.Min(a, b) // m/s, slow..fast
	case "speed":
		t.kind = "speed"
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		t.low, t.hi = math.Min(a, b)/3.6, math.Max(a, b)/3.6
	case "cadence":
		t.kind = "cadence"
		t.low, _ = strconv.ParseFloat(m[1], 64)
		t.hi, _ = strconv.ParseFloat(m[2], 64)
	case "power":
		t.kind = "power"
		t.low, _ = strconv.ParseFloat(m[1], 64)
		t.hi, _ = strconv.ParseFloat(m[2], 64)
	default:
		return target{}, fmt.Errorf("unknown target %q (hr, hr zone, pace, speed, cadence, power)", s)
	}
	if t.low > t.hi {
		t.low, t.hi = t.hi, t.low
	}
	return t, nil
}

// Validate checks a spec and normalises sport/step names.
func (w *WorkoutSpec) Validate() error {
	if strings.TrimSpace(w.Name) == "" {
		return errors.New("workout needs a name")
	}
	w.Sport = strings.ToLower(strings.TrimSpace(w.Sport))
	if w.Sport == "" {
		w.Sport = "running"
	}
	if _, ok := sportIDs[w.Sport]; !ok {
		return fmt.Errorf("unknown sport %q", w.Sport)
	}
	if len(w.Steps) == 0 {
		return errors.New("workout needs steps")
	}
	var check func(steps []StepSpec, depth int) error
	check = func(steps []StepSpec, depth int) error {
		for i := range steps {
			st := &steps[i]
			if st.Repeat > 0 {
				if depth > 0 {
					return errors.New("repeat groups cannot be nested")
				}
				if len(st.Steps) == 0 {
					return errors.New("a repeat group needs steps")
				}
				if err := check(st.Steps, depth+1); err != nil {
					return err
				}
				continue
			}
			st.Type = strings.ToLower(strings.TrimSpace(st.Type))
			if st.Type == "" {
				st.Type = "interval"
			}
			if _, ok := stepIDs[st.Type]; !ok {
				return fmt.Errorf("unknown step type %q", st.Type)
			}
			n := 0
			for _, has := range []bool{st.Duration != "", st.Distance != "", st.Calories > 0, st.Lap} {
				if has {
					n++
				}
			}
			if n == 0 {
				st.Lap = true
			} else if n > 1 {
				return fmt.Errorf("step %q: give one of duration, distance, calories or lap", st.Type)
			}
			if st.Duration != "" {
				if _, err := parseDurationSec(st.Duration); err != nil {
					return err
				}
			}
			if st.Distance != "" {
				if _, err := parseDistanceM(st.Distance); err != nil {
					return err
				}
			}
			if _, err := parseTarget(st.Target); err != nil {
				return err
			}
		}
		return nil
	}
	return check(w.Steps, 0)
}

// ConnectJSON renders the spec in Garmin Connect's workout format.
func (w *WorkoutSpec) ConnectJSON() (map[string]any, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	sp := sportIDs[w.Sport]
	sport := map[string]any{"sportTypeId": sp[0], "sportTypeKey": sp[1]}
	order := 0
	est := 0.0
	var conv func(steps []StepSpec) []map[string]any
	conv = func(steps []StepSpec) []map[string]any {
		var out []map[string]any
		for _, st := range steps {
			order++
			if st.Repeat > 0 {
				g := map[string]any{"type": "RepeatGroupDTO", "stepOrder": order, "stepType": map[string]any{"stepTypeId": 6, "stepTypeKey": "repeat"},
					"numberOfIterations": st.Repeat, "smartRepeat": false,
					"endCondition": map[string]any{"conditionTypeId": 7, "conditionTypeKey": "iterations", "displayable": false}, "endConditionValue": float64(st.Repeat)}
				before := est
				g["workoutSteps"] = conv(st.Steps)
				est += (est - before) * float64(st.Repeat-1)
				out = append(out, g)
				continue
			}
			id := stepIDs[st.Type]
			e := map[string]any{"type": "ExecutableStepDTO", "stepOrder": order, "stepType": map[string]any{"stepTypeId": id[0], "stepTypeKey": id[1]}}
			if st.Name != "" {
				e["description"] = st.Name
			}
			switch {
			case st.Duration != "":
				sec, _ := parseDurationSec(st.Duration)
				e["endCondition"] = map[string]any{"conditionTypeId": 2, "conditionTypeKey": "time"}
				e["endConditionValue"] = sec
				est += sec
			case st.Distance != "":
				m, _ := parseDistanceM(st.Distance)
				e["endCondition"] = map[string]any{"conditionTypeId": 3, "conditionTypeKey": "distance"}
				e["endConditionValue"] = m
				est += m / 1000 * 360
			case st.Calories > 0:
				e["endCondition"] = map[string]any{"conditionTypeId": 4, "conditionTypeKey": "calories"}
				e["endConditionValue"] = float64(st.Calories)
				est += float64(st.Calories) * 6
			default:
				e["endCondition"] = map[string]any{"conditionTypeId": 1, "conditionTypeKey": "lap.button"}
				est += 300
			}
			t, _ := parseTarget(st.Target)
			switch t.kind {
			case "hr":
				e["targetType"] = map[string]any{"workoutTargetTypeId": 4, "workoutTargetTypeKey": "heart.rate.zone"}
				e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
			case "hrzone":
				e["targetType"] = map[string]any{"workoutTargetTypeId": 4, "workoutTargetTypeKey": "heart.rate.zone"}
				e["zoneNumber"] = t.zone
			case "pace":
				e["targetType"] = map[string]any{"workoutTargetTypeId": 6, "workoutTargetTypeKey": "pace.zone"}
				e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
			case "speed":
				e["targetType"] = map[string]any{"workoutTargetTypeId": 5, "workoutTargetTypeKey": "speed.zone"}
				e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
			case "cadence":
				e["targetType"] = map[string]any{"workoutTargetTypeId": 3, "workoutTargetTypeKey": "cadence"}
				e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
			case "power":
				e["targetType"] = map[string]any{"workoutTargetTypeId": 2, "workoutTargetTypeKey": "power.zone"}
				e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
			default:
				e["targetType"] = map[string]any{"workoutTargetTypeId": 1, "workoutTargetTypeKey": "no.target"}
			}
			out = append(out, e)
		}
		return out
	}
	steps := conv(w.Steps)
	doc := map[string]any{"workoutName": w.Name, "sportType": sport, "estimatedDurationInSecs": int(est),
		"workoutSegments": []map[string]any{{"segmentOrder": 1, "sportType": sport, "workoutSteps": steps}}}
	if w.Description != "" {
		doc["description"] = w.Description
	}
	return doc, nil
}

// FIT renders the spec as a workout file the watch reads from NewFiles.
func (w *WorkoutSpec) FIT(unitID uint64, now time.Time) ([]byte, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	b := fit.NewBuilder(21217, true)
	_ = b.Write(0, []fit.FieldDef{{Num: 0, Size: 1, BaseType: fit.BaseEnum}, {Num: 1, Size: 2, BaseType: fit.BaseUint16}, {Num: 2, Size: 2, BaseType: fit.BaseUint16}, {Num: 4, Size: 4, BaseType: fit.BaseUint32}, {Num: 3, Size: 4, BaseType: fit.BaseUint32z}},
		map[uint8]any{0: 5, 1: 1, 2: 65534, 4: fit.Timestamp(now), 3: unitID})
	// Flatten: repeat groups become a "repeat from step N" step after their body.
	type flat struct {
		st       StepSpec
		repeatTo int
		repeatN  int
	}
	var steps []flat
	for _, st := range w.Steps {
		if st.Repeat > 0 {
			from := len(steps)
			for _, s2 := range st.Steps {
				steps = append(steps, flat{st: s2})
			}
			steps = append(steps, flat{repeatTo: from, repeatN: st.Repeat})
			continue
		}
		steps = append(steps, flat{st: st})
	}
	name := w.Name
	if len(name) > 15 {
		name = string([]rune(name)[:15])
	}
	_ = b.Write(26, []fit.FieldDef{{Num: 254, Size: 2, BaseType: fit.BaseUint16}, {Num: 4, Size: 1, BaseType: fit.BaseEnum}, {Num: 11, Size: 1, BaseType: fit.BaseEnum}, {Num: 6, Size: 2, BaseType: fit.BaseUint16}, {Num: 8, Size: 16, BaseType: fit.BaseString}},
		map[uint8]any{254: 0, 4: fitSport[w.Sport], 11: 0, 6: len(steps), 8: name})
	defs := []fit.FieldDef{{Num: 254, Size: 2, BaseType: fit.BaseUint16}, {Num: 0, Size: 16, BaseType: fit.BaseString}, {Num: 1, Size: 1, BaseType: fit.BaseEnum}, {Num: 2, Size: 4, BaseType: fit.BaseUint32}, {Num: 3, Size: 1, BaseType: fit.BaseEnum}, {Num: 4, Size: 4, BaseType: fit.BaseUint32}, {Num: 5, Size: 4, BaseType: fit.BaseUint32}, {Num: 6, Size: 4, BaseType: fit.BaseUint32}, {Num: 7, Size: 1, BaseType: fit.BaseEnum}}
	for i, f := range steps {
		v := map[uint8]any{254: i}
		if f.repeatN > 0 {
			v[1], v[2], v[3], v[4], v[7] = 6, f.repeatTo, 2, f.repeatN, 0 // repeat_until_steps_cmplt, open target, count
			_ = b.Write(27, defs, v)
			continue
		}
		st := f.st
		if st.Name != "" {
			n := st.Name
			if len(n) > 15 {
				n = string([]rune(n)[:15])
			}
			v[0] = n
		}
		switch {
		case st.Duration != "":
			sec, _ := parseDurationSec(st.Duration)
			v[1], v[2] = 0, uint32(sec*1000)
		case st.Distance != "":
			m, _ := parseDistanceM(st.Distance)
			v[1], v[2] = 1, uint32(m*100)
		case st.Calories > 0:
			v[1], v[2] = 4, uint32(st.Calories)
		default:
			v[1] = 5 // open: lap button
		}
		t, _ := parseTarget(st.Target)
		switch t.kind {
		case "hr":
			v[3], v[4], v[5], v[6] = 1, 0, uint32(t.low+100), uint32(t.hi+100)
		case "hrzone":
			v[3], v[4] = 1, t.zone
		case "pace", "speed":
			v[3], v[4], v[5], v[6] = 0, 0, uint32(t.low*1000), uint32(t.hi*1000)
		case "cadence":
			v[3], v[4], v[5], v[6] = 3, 0, uint32(t.low), uint32(t.hi)
		case "power":
			v[3], v[4], v[5], v[6] = 4, 0, uint32(t.low+1000), uint32(t.hi+1000)
		default:
			v[3] = 2
		}
		v[7] = fitIntens[st.Type]
		_ = b.Write(27, defs, v)
	}
	return b.Bytes(), nil
}

// WriteWorkoutToWatch writes the workout file into NewFiles (no cloud).
func (s *Service) WriteWorkoutToWatch(ctx context.Context, spec *WorkoutSpec) (string, error) {
	dx, err := s.DeviceXML(ctx)
	if err != nil {
		return "", err
	}
	data, err := spec.FIT(dx.UnitID, time.Now())
	if err != nil {
		return "", err
	}
	p := PathNewFiles + "/" + newUUID() + ".FIT"
	if err := s.dev.Write(ctx, p, data); err != nil {
		return "", err
	}
	return p, nil
}

// CloudWorkout is a workout stored in Garmin Connect.
type CloudWorkout struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Sport    string `json:"sport"`
	Duration int    `json:"estimatedSec"`
	Updated  string `json:"updated,omitempty"`
}

// Workouts lists the workouts in Garmin Connect.
func (c *Cloud) Workouts(ctx context.Context, limit int) ([]CloudWorkout, error) {
	if limit <= 0 {
		limit = 50
	}
	var raw []map[string]any
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/workout-service/workouts?start=0&limit=%d&myWorkoutsOnly=true", limit), &raw); err != nil {
		return nil, err
	}
	out := make([]CloudWorkout, 0, len(raw))
	for _, m := range raw {
		w := CloudWorkout{ID: int64(num(m["workoutId"])), Name: fmt.Sprint(m["workoutName"]), Duration: asInt(m, "estimatedDurationInSecs")}
		if st, ok := m["sportType"].(map[string]any); ok {
			w.Sport, _ = st["sportTypeKey"].(string)
		}
		if u, ok := m["updatedDate"].(string); ok && len(u) >= 10 {
			w.Updated = u[:10]
		}
		out = append(out, w)
	}
	return out, nil
}

// CreateWorkout stores a workout in Garmin Connect and returns its id.
func (c *Cloud) CreateWorkout(ctx context.Context, spec *WorkoutSpec) (*CloudWorkout, error) {
	doc, err := spec.ConnectJSON()
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := c.gc.SendJSON(ctx, http.MethodPost, "/workout-service/workout", doc, &resp); err != nil {
		return nil, err
	}
	w := &CloudWorkout{ID: int64(num(resp["workoutId"])), Name: spec.Name, Sport: spec.Sport, Duration: asInt(resp, "estimatedDurationInSecs")}
	if w.ID == 0 {
		return nil, errors.New("Garmin Connect did not return a workout id")
	}
	return w, nil
}

// SendWorkout asks Connect to queue a workout for the watch, then pulls it.
func (c *Cloud) SendWorkout(ctx context.Context, id int64, name string) ([]PulledFile, error) {
	dx, err := c.svc.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}
	body := []map[string]any{{"deviceId": dx.UnitID, "messageUrl": fmt.Sprintf("workout-service/workout/FIT/%d", id), "messageType": "workouts", "groupName": nil, "messageName": name, "priority": 1, "fileType": "FIT", "metaDataId": id}}
	if err := c.gc.SendJSON(ctx, http.MethodPost, "/device-service/devicemessage/messages", body, nil); err != nil {
		return nil, err
	}
	var pulled []PulledFile
	for attempt := 0; attempt < 4; attempt++ {
		p, err := c.Pull(ctx, func(m DeviceMessage) bool { return m.MessageType == "workouts" })
		if err != nil {
			return nil, err
		}
		pulled = append(pulled, p...)
		if len(p) > 0 {
			break
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return pulled, nil
}

// DeleteWorkout removes a workout from Garmin Connect.
func (c *Cloud) DeleteWorkout(ctx context.Context, id int64) error {
	return c.gc.SendJSON(ctx, http.MethodDelete, fmt.Sprintf("/workout-service/workout/%d", id), nil, nil)
}
