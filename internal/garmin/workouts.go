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
	"unicode/utf8"

	"github.com/DevWolk/garminctl/internal/fit"
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

// Target kinds as parseTarget names them.
const (
	targetNone    = "none"
	targetHR      = "hr"
	targetHRZone  = "hrzone"
	targetPace    = "pace"
	targetSpeed   = "speed"
	targetCadence = "cadence"
	targetPower   = "power"
)

// Step types and sports that appear in several tables.
const (
	stepInterval = "interval"
	sportRunning = "running"
	kindOther    = "other" // both a sport and a step type
)

// maxStepCalories bounds a calorie step so it fits the FIT uint32 slot with room to spare.
const maxStepCalories = 100000

var (
	sportIDs   = map[string][2]any{sportRunning: {1, sportRunning}, "cycling": {2, "cycling"}, kindOther: {3, kindOther}, "swimming": {4, "swimming"}, "strength": {5, "strength_training"}, "cardio": {6, "cardio_training"}, "yoga": {7, "yoga"}, "pilates": {8, "pilates"}, "hiit": {9, "hiit"}, "walking": {17, "walking"}, "hiking": {18, "hiking"}}
	stepIDs    = map[string][2]any{"warmup": {1, "warmup"}, "cooldown": {2, "cooldown"}, stepInterval: {3, stepInterval}, "recovery": {4, "recovery"}, "rest": {5, "rest"}, kindOther: {7, kindOther}, "main": {8, "main"}}
	fitSport   = map[string]int{sportRunning: 1, "cycling": 2, kindOther: 0, "swimming": 5, "strength": 10, "cardio": 4, "yoga": 10, "pilates": 10, "hiit": 10, "walking": 11, "hiking": 17}
	fitIntens  = map[string]int{"warmup": 2, "cooldown": 3, stepInterval: 5, "recovery": 4, "rest": 1, kindOther: 6, "main": 0}
	reRange    = regexp.MustCompile(`^(\S+)\s*[-–]\s*(\S+)$`)
	reDuration = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(h|m|min|s|sec)?`)
	reDistance = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(km|k|m|mi|miles?|yd)?$`)
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

	total := 0.0
	matched := false

	for _, m := range reDuration.FindAllStringSubmatch(s, -1) {
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

	m := reDistance.FindStringSubmatch(s)
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

// rangeTargets maps the words a target may start with to its kind.
var rangeTargets = map[string]string{
	"hr": targetHR, "heart": targetHR, "heartrate": targetHR, "bpm": targetHR,
	targetPace: targetPace, targetSpeed: targetSpeed, targetCadence: targetCadence, targetPower: targetPower,
}

func parseTarget(s string) (target, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == targetNone || s == "no target" {
		return target{kind: targetNone}, nil
	}

	kind := strings.Fields(s)[0]

	rest := strings.TrimSpace(strings.TrimPrefix(s, kind))
	if isZoneTarget(kind, rest) {
		return parseZoneTarget(s, rest)
	}

	m := reRange.FindStringSubmatch(rest)
	if m == nil {
		return target{}, fmt.Errorf("target %q needs a range like 140-160", s)
	}

	tk, ok := rangeTargets[kind]
	if !ok {
		return target{}, fmt.Errorf("unknown target %q (hr, hr zone, pace, speed, cadence, power)", s)
	}

	t, err := rangeTarget(tk, m[1], m[2], s)
	if err != nil {
		return target{}, err
	}

	if t.low > t.hi {
		t.low, t.hi = t.hi, t.low
	}

	return t, nil
}

// isZoneTarget recognises "zone 3", "hr zone 3" and "heart rate zone 3".
func isZoneTarget(kind, rest string) bool {
	return kind == "zone" || (kind == "hr" || kind == "heart") && strings.HasPrefix(rest, "zone")
}

func parseZoneTarget(spec, rest string) (target, error) {
	z, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(rest, "zone"), "rate zone")))
	if err != nil {
		z, err = strconv.Atoi(strings.TrimSpace(rest))
	}

	if err != nil || z < 1 || z > 5 {
		return target{}, fmt.Errorf("bad heart-rate zone in %q", spec)
	}

	return target{kind: targetHRZone, zone: z}, nil
}

// rangeTarget converts the two ends of a range into the target's unit.
func rangeTarget(kind, lo, hi, spec string) (target, error) {
	t := target{kind: kind}

	switch kind {
	case targetPace:
		a, err1 := parsePaceSec(lo)

		b, err2 := parsePaceSec(hi)
		if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
			return target{}, fmt.Errorf("bad pace in %q (use min:sec per km)", spec)
		}

		t.low, t.hi = 1000/math.Max(a, b), 1000/math.Min(a, b) // m/s, slow..fast
	case targetSpeed:
		a, _ := strconv.ParseFloat(lo, 64)
		b, _ := strconv.ParseFloat(hi, 64)
		t.low, t.hi = math.Min(a, b)/3.6, math.Max(a, b)/3.6
	default: // hr, cadence, power: plain numbers
		t.low, _ = strconv.ParseFloat(lo, 64)
		t.hi, _ = strconv.ParseFloat(hi, 64)
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
		w.Sport = sportRunning
	}

	if _, ok := sportIDs[w.Sport]; !ok {
		return fmt.Errorf("unknown sport %q", w.Sport)
	}

	if len(w.Steps) == 0 {
		return errors.New("workout needs steps")
	}

	return validateSteps(w.Steps, 0)
}

// validateSteps checks a step list; repeat groups may not nest.
func validateSteps(steps []StepSpec, depth int) error {
	for i := range steps {
		st := &steps[i]
		if st.Repeat > 0 {
			if depth > 0 {
				return errors.New("repeat groups cannot be nested")
			}

			if len(st.Steps) == 0 {
				return errors.New("a repeat group needs steps")
			}

			if err := validateSteps(st.Steps, depth+1); err != nil {
				return err
			}

			continue
		}

		if err := validateStep(st); err != nil {
			return err
		}
	}

	return nil
}

// validateStep normalises one executable step and checks its parts parse.
func validateStep(st *StepSpec) error {
	st.Type = strings.ToLower(strings.TrimSpace(st.Type))
	if st.Type == "" {
		st.Type = stepInterval
	}

	if _, ok := stepIDs[st.Type]; !ok {
		return fmt.Errorf("unknown step type %q", st.Type)
	}

	if st.Calories < 0 || st.Calories > maxStepCalories {
		return fmt.Errorf("step %q: calories must be between 1 and %d", st.Type, maxStepCalories)
	}

	n := 0

	for _, has := range []bool{st.Duration != "", st.Distance != "", st.Calories > 0, st.Lap} {
		if has {
			n++
		}
	}

	switch {
	case n == 0:
		st.Lap = true
	case n > 1:
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

	_, err := parseTarget(st.Target)

	return err
}

// endCondition and targetType build the small enum objects of Connect's
// workout JSON.
func endCondition(id int, key string) map[string]any {
	return map[string]any{"conditionTypeId": id, "conditionTypeKey": key}
}

func targetType(id int, key string) map[string]any {
	return map[string]any{"workoutTargetTypeId": id, "workoutTargetTypeKey": key}
}

// connectSteps numbers the steps of a workout and estimates its duration
// while rendering Connect's JSON.
type connectSteps struct {
	order int
	est   float64 // seconds
}

func (b *connectSteps) convert(steps []StepSpec) []map[string]any {
	var out []map[string]any

	for _, st := range steps {
		b.order++

		if st.Repeat > 0 {
			out = append(out, b.repeat(st))
			continue
		}

		out = append(out, b.step(st))
	}

	return out
}

func (b *connectSteps) repeat(st StepSpec) map[string]any {
	ec := endCondition(7, "iterations")
	ec["displayable"] = false

	g := map[string]any{
		"type": "RepeatGroupDTO", "stepOrder": b.order, "stepType": map[string]any{"stepTypeId": 6, "stepTypeKey": "repeat"},
		"numberOfIterations": st.Repeat, "smartRepeat": false,
		"endCondition": ec, "endConditionValue": float64(st.Repeat),
	}

	before := b.est
	g["workoutSteps"] = b.convert(st.Steps)
	b.est += (b.est - before) * float64(st.Repeat-1)

	return g
}

func (b *connectSteps) step(st StepSpec) map[string]any {
	id := stepIDs[st.Type]

	e := map[string]any{"type": "ExecutableStepDTO", "stepOrder": b.order, "stepType": map[string]any{"stepTypeId": id[0], "stepTypeKey": id[1]}}
	if st.Name != "" {
		e["description"] = st.Name
	}

	b.end(e, st)
	connectTarget(e, st.Target)

	return e
}

// end sets the step's end condition and adds its estimated seconds.
func (b *connectSteps) end(e map[string]any, st StepSpec) {
	switch {
	case st.Duration != "":
		sec, _ := parseDurationSec(st.Duration)
		e["endCondition"] = endCondition(2, "time")
		e["endConditionValue"] = sec
		b.est += sec
	case st.Distance != "":
		m, _ := parseDistanceM(st.Distance)
		e["endCondition"] = endCondition(3, "distance")
		e["endConditionValue"] = m
		b.est += m / 1000 * 360
	case st.Calories > 0:
		e["endCondition"] = endCondition(4, "calories")
		e["endConditionValue"] = float64(st.Calories)
		b.est += float64(st.Calories) * 6
	default:
		e["endCondition"] = endCondition(1, "lap.button")
		b.est += 300
	}
}

// connectTarget sets the step's target in Connect's form.
func connectTarget(e map[string]any, spec string) {
	t, _ := parseTarget(spec)

	switch t.kind {
	case targetHR:
		e["targetType"] = targetType(4, "heart.rate.zone")
		e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
	case targetHRZone:
		e["targetType"] = targetType(4, "heart.rate.zone")
		e["zoneNumber"] = t.zone
	case targetPace:
		e["targetType"] = targetType(6, "pace.zone")
		e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
	case targetSpeed:
		e["targetType"] = targetType(5, "speed.zone")
		e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
	case targetCadence:
		e["targetType"] = targetType(3, "cadence")
		e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
	case targetPower:
		e["targetType"] = targetType(2, "power.zone")
		e["targetValueOne"], e["targetValueTwo"] = t.low, t.hi
	default:
		e["targetType"] = targetType(1, "no.target")
	}
}

// ConnectJSON renders the spec in Garmin Connect's workout format.
func (w *WorkoutSpec) ConnectJSON() (map[string]any, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}

	sp := sportIDs[w.Sport]
	sport := map[string]any{"sportTypeId": sp[0], "sportTypeKey": sp[1]}
	b := &connectSteps{}
	steps := b.convert(w.Steps)

	doc := map[string]any{
		"workoutName": w.Name, "sportType": sport, "estimatedDurationInSecs": int(b.est),
		"workoutSegments": []map[string]any{{"segmentOrder": 1, "sportType": sport, "workoutSteps": steps}},
	}
	if w.Description != "" {
		doc["description"] = w.Description
	}

	return doc, nil
}

// flatStep is one workout_step message: an executable step, or a "repeat
// from step N" marker after the body of a repeat group.
type flatStep struct {
	st       StepSpec
	repeatTo int
	repeatN  int
}

// flattenSteps lays repeat groups out the way the FIT file wants them.
func flattenSteps(in []StepSpec) []flatStep {
	var steps []flatStep

	for _, st := range in {
		if st.Repeat > 0 {
			from := len(steps)
			for _, s2 := range st.Steps {
				steps = append(steps, flatStep{st: s2})
			}

			steps = append(steps, flatStep{repeatTo: from, repeatN: st.Repeat})

			continue
		}

		steps = append(steps, flatStep{st: st})
	}

	return steps
}

// fitName fits a label into a 16-byte FIT string field (15 bytes plus NUL),
// cutting on a character boundary.
func fitName(s string) string {
	const maxBytes = 15
	if len(s) <= maxBytes {
		return s
	}

	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

var (
	fitFileIDDefs  = []fit.FieldDef{{Num: 0, Size: 1, BaseType: fit.BaseEnum}, {Num: 1, Size: 2, BaseType: fit.BaseUint16}, {Num: 2, Size: 2, BaseType: fit.BaseUint16}, {Num: 4, Size: 4, BaseType: fit.BaseUint32}, {Num: 3, Size: 4, BaseType: fit.BaseUint32z}}
	fitWorkoutDefs = []fit.FieldDef{{Num: 254, Size: 2, BaseType: fit.BaseUint16}, {Num: 4, Size: 1, BaseType: fit.BaseEnum}, {Num: 11, Size: 1, BaseType: fit.BaseEnum}, {Num: 6, Size: 2, BaseType: fit.BaseUint16}, {Num: 8, Size: 16, BaseType: fit.BaseString}}
	fitStepDefs    = []fit.FieldDef{{Num: 254, Size: 2, BaseType: fit.BaseUint16}, {Num: 0, Size: 16, BaseType: fit.BaseString}, {Num: 1, Size: 1, BaseType: fit.BaseEnum}, {Num: 2, Size: 4, BaseType: fit.BaseUint32}, {Num: 3, Size: 1, BaseType: fit.BaseEnum}, {Num: 4, Size: 4, BaseType: fit.BaseUint32}, {Num: 5, Size: 4, BaseType: fit.BaseUint32}, {Num: 6, Size: 4, BaseType: fit.BaseUint32}, {Num: 7, Size: 1, BaseType: fit.BaseEnum}}
)

// FIT renders the spec as a workout file the watch reads from NewFiles.
func (w *WorkoutSpec) FIT(unitID uint64, now time.Time) ([]byte, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}

	b := fit.NewBuilder(21217, true)
	if err := b.Write(0, fitFileIDDefs, map[uint8]any{0: 5, 1: 1, 2: 65534, 4: fit.Timestamp(now), 3: fitSerial(unitID)}); err != nil {
		return nil, err
	}

	steps := flattenSteps(w.Steps)
	if err := b.Write(26, fitWorkoutDefs, map[uint8]any{254: 0, 4: fitSport[w.Sport], 11: 0, 6: len(steps), 8: fitName(w.Name)}); err != nil {
		return nil, err
	}

	for i, f := range steps {
		if err := b.Write(27, fitStepDefs, fitStepValues(i, f)); err != nil {
			return nil, err
		}
	}

	return b.Bytes(), nil
}

// fitStepValues fills one workout_step message (see docs/formats.md for the
// duration, target and intensity codes).
func fitStepValues(i int, f flatStep) map[uint8]any {
	v := map[uint8]any{254: i}
	if f.repeatN > 0 {
		v[1], v[2], v[3], v[4], v[7] = 6, f.repeatTo, 2, f.repeatN, 0 // repeat_until_steps_cmplt, open target, count

		return v
	}

	st := f.st
	if st.Name != "" {
		v[0] = fitName(st.Name)
	}

	fitDuration(v, st)
	fitTarget(v, st.Target)

	v[7] = fitIntens[st.Type]

	return v
}

func fitDuration(v map[uint8]any, st StepSpec) {
	switch {
	case st.Duration != "":
		sec, _ := parseDurationSec(st.Duration)
		v[1], v[2] = 0, uint32(sec*1000)
	case st.Distance != "":
		m, _ := parseDistanceM(st.Distance)
		v[1], v[2] = 1, uint32(m*100)
	case st.Calories > 0:
		v[1], v[2] = 4, uint32(st.Calories) //nolint:gosec // Validate bounds calories to maxStepCalories
	default:
		v[1] = 5 // open: lap button
	}
}

func fitTarget(v map[uint8]any, spec string) {
	t, _ := parseTarget(spec)

	switch t.kind {
	case targetHR:
		v[3], v[4], v[5], v[6] = 1, 0, uint32(t.low+100), uint32(t.hi+100)
	case targetHRZone:
		v[3], v[4] = 1, t.zone
	case targetPace, targetSpeed:
		v[3], v[4], v[5], v[6] = 0, 0, uint32(t.low*1000), uint32(t.hi*1000)
	case targetCadence:
		v[3], v[4], v[5], v[6] = 3, 0, uint32(t.low), uint32(t.hi)
	case targetPower:
		v[3], v[4], v[5], v[6] = 4, 0, uint32(t.low+1000), uint32(t.hi+1000)
	default:
		v[3] = 2
	}
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
		return nil, errors.New("no workout id in the Garmin Connect answer")
	}

	return w, nil
}

// SendWorkout asks Connect to queue a workout for the watch, then pulls it.
func (c *Cloud) SendWorkout(ctx context.Context, id int64, name string) ([]PulledFile, error) {
	dx, err := c.deviceXML(ctx)
	if err != nil {
		return nil, err
	}

	body := []map[string]any{{"deviceId": dx.UnitID, "messageUrl": fmt.Sprintf("workout-service/workout/FIT/%d", id), "messageType": "workouts", "groupName": nil, "messageName": name, "priority": 1, "fileType": "FIT", "metaDataId": id}}
	if err := c.gc.SendJSON(ctx, http.MethodPost, "/device-service/devicemessage/messages", body, nil); err != nil {
		return nil, err
	}

	pulled, err := c.pullQueued(ctx, "workouts")
	if err != nil {
		return nil, err
	}

	return pulled, nil
}

// DeleteWorkout removes a workout from Garmin Connect.
func (c *Cloud) DeleteWorkout(ctx context.Context, id int64) error {
	return c.gc.SendJSON(ctx, http.MethodDelete, fmt.Sprintf("/workout-service/workout/%d", id), nil, nil)
}
