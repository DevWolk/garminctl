package garmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/paths"
)

// ---------- activities from the files on the watch ----------

// ActivitySummary is the session of one activity file.
type ActivitySummary struct {
	File       string    `json:"file"`
	Sport      string    `json:"sport"`
	SubSport   string    `json:"subSport,omitempty"`
	Start      time.Time `json:"start"`
	Elapsed    float64   `json:"elapsedSec"`
	Timer      float64   `json:"timerSec"`
	Distance   float64   `json:"distanceM"`
	Calories   int       `json:"calories"`
	AvgHR      int       `json:"avgHr"`
	MaxHR      int       `json:"maxHr"`
	AvgSpeed   float64   `json:"avgSpeedMs"`
	Ascent     int       `json:"ascentM"`
	Descent    int       `json:"descentM"`
	AvgCadence int       `json:"avgCadence"`
	HasGPS     bool      `json:"hasGps"`
	Size       int64     `json:"size"`
}

type activityCache struct {
	Entries map[string]ActivitySummary `json:"entries"` // file -> summary (Size guards staleness)
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case uint32:
		return float64(t)
	case uint16:
		return float64(t)
	case uint8:
		return float64(t)
	case int32:
		return float64(t)
	case int16:
		return float64(t)
	case int8:
		return float64(t)
	}

	return math.NaN()
}

func fieldNum(m *fit.NamedMessage, name string) float64 {
	f, ok := m.FieldByName(name)
	if !ok {
		return math.NaN()
	}

	return num(f.Value)
}

func fieldStr(m *fit.NamedMessage, name string) string {
	f, ok := m.FieldByName(name)
	if !ok || f.Value == nil {
		return ""
	}

	return fmt.Sprint(f.Value)
}

func semicircles(v float64) float64 { return v * 180 / 2147483648 }

func (s *Service) activityCachePath(unit uint64) string {
	return filepath.Join(paths.Cache(), fmt.Sprintf("activities-%d.json", unit))
}

// summarize decodes the session of one activity file.
func (s *Service) summarize(data []byte, name string) (ActivitySummary, error) {
	f, err := fit.Decode(data)
	if err != nil {
		return ActivitySummary{}, err
	}

	sess := f.MessagesByNum(18)
	if len(sess) == 0 {
		return ActivitySummary{}, errors.New("no session message")
	}

	m := s.profile.DescribeMessage(sess[len(sess)-1])

	a := ActivitySummary{File: name, Sport: fieldStr(&m, "sport"), SubSport: fieldStr(&m, "subSport"), Size: int64(len(data))}
	if a.SubSport == "generic" {
		a.SubSport = ""
	}

	if st := fieldNum(&m, "startTime"); !math.IsNaN(st) {
		a.Start = fit.TimestampTime(uint32(st))
	} else if ts := fieldNum(&m, "timestamp"); !math.IsNaN(ts) {
		a.Start = fit.TimestampTime(uint32(ts))
	}

	get := func(n string) float64 {
		v := fieldNum(&m, n)
		if math.IsNaN(v) {
			return 0
		}

		return v
	}
	a.Elapsed, a.Timer, a.Distance = get("totalElapsedTime"), get("totalTimerTime"), get("totalDistance")
	a.Calories, a.AvgHR, a.MaxHR = int(get("totalCalories")), int(get("avgHeartRate")), int(get("maxHeartRate"))

	a.AvgSpeed = get("enhancedAvgSpeed")
	if a.AvgSpeed == 0 {
		a.AvgSpeed = get("avgSpeed")
	}

	a.Ascent, a.Descent, a.AvgCadence = int(get("totalAscent")), int(get("totalDescent")), int(get("avgCadence"))
	a.HasGPS = !math.IsNaN(fieldNum(&m, "startPositionLat"))

	return a, nil
}

// ActivityList is the result of Activities: what is known so far.
type ActivityList struct {
	Activities []ActivitySummary `json:"activities"`
	Pending    int               `json:"pending"` // files not decoded yet
	Total      int               `json:"total"`
}

// activityJobs tracks the one background decode per watch.
var activityJobs = struct {
	sync.Mutex
	running map[uint64]bool
}{running: map[uint64]bool{}}

// activityCacheMu serialises access to the on-disk activity caches, which
// every Service for the same watch shares.
var activityCacheMu sync.Mutex

// loadActivityCache reads a cache file; a missing or damaged file is empty.
// The caller holds activityCacheMu.
func loadActivityCache(cp string) activityCache {
	cache := activityCache{Entries: map[string]ActivitySummary{}}

	b, err := os.ReadFile(cp) //nolint:gosec // path is inside garminctl's cache directory
	if err == nil {
		_ = json.Unmarshal(b, &cache)
	}

	if cache.Entries == nil {
		cache.Entries = map[string]ActivitySummary{}
	}

	return cache
}

// saveActivityCache writes the cache atomically. The caller holds activityCacheMu.
func saveActivityCache(cp string, cache activityCache) {
	b, err := json.MarshalIndent(cache, "", " ")
	if err != nil {
		return
	}

	_ = os.MkdirAll(filepath.Dir(cp), 0o700)

	tmp := cp + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		_ = os.Rename(tmp, cp)
	}
}

// decodeActivities reads and summarises the named files. The cache is read
// once and rewritten after every file, so a concurrent Activities call sees
// the progress; only one decode runs per watch (startActivityJob).
func (s *Service) decodeActivities(ctx context.Context, cp string, names []string) {
	activityCacheMu.Lock()
	cache := loadActivityCache(cp)
	activityCacheMu.Unlock()

	for _, n := range names {
		if ctx.Err() != nil {
			return
		}

		data, err := s.dev.Read(ctx, "GARMIN/Activity/"+n)
		if err != nil {
			continue
		}

		a, err := s.summarize(data, n)
		if err != nil {
			a = ActivitySummary{File: n, Sport: "unreadable", Size: int64(len(data))}
		}

		cache.Entries[a.File] = a

		activityCacheMu.Lock()
		saveActivityCache(cp, cache)
		activityCacheMu.Unlock()
	}
}

// startActivityJob decodes todo in the background unless a job for this
// watch is already running. The job outlives the request that started it.
func (s *Service) startActivityJob(ctx context.Context, unit uint64, cp string, todo []string) {
	activityJobs.Lock()
	defer activityJobs.Unlock()

	if activityJobs.running[unit] {
		return
	}

	activityJobs.running[unit] = true

	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Hour)

	go func() {
		defer cancel()
		defer func() {
			activityJobs.Lock()
			activityJobs.running[unit] = false
			activityJobs.Unlock()
		}()

		s.decodeActivities(bctx, cp, todo)
	}()
}

// Activities lists the activities on the watch. Files not decoded before are
// decoded in the background (reading 200 files over USB takes a while);
// call again until Pending is 0.
func (s *Service) Activities(ctx context.Context, wait bool) (*ActivityList, error) {
	dx, err := s.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := s.dev.List(ctx, "GARMIN/Activity")
	if err != nil {
		return nil, err
	}

	cp := s.activityCachePath(dx.UnitID)

	activityCacheMu.Lock()
	cache := loadActivityCache(cp)
	activityCacheMu.Unlock()

	var todo []string

	out := &ActivityList{}

	for _, e := range entries {
		if e.Dir || !strings.EqualFold(path.Ext(e.Name), ".fit") {
			continue
		}

		out.Total++
		if c, ok := cache.Entries[e.Name]; ok && c.Size == e.Size {
			out.Activities = append(out.Activities, c)
		} else {
			todo = append(todo, e.Name)
		}
	}

	if len(todo) > 0 && wait {
		s.decodeActivities(ctx, cp, todo)

		return s.Activities(ctx, false)
	}

	if len(todo) > 0 {
		s.startActivityJob(ctx, dx.UnitID, cp, todo)
		out.Pending = len(todo)
	}

	sort.Slice(out.Activities, func(i, j int) bool { return out.Activities[i].Start.After(out.Activities[j].Start) })

	return out, nil
}

// TrackPoint is one downsampled record of an activity.
type TrackPoint struct {
	T    float64 `json:"t"` // seconds since start
	Lat  float64 `json:"lat,omitempty"`
	Lon  float64 `json:"lon,omitempty"`
	HR   int     `json:"hr,omitempty"`
	Spd  float64 `json:"spd,omitempty"` // m/s
	Alt  float64 `json:"alt,omitempty"`
	Dist float64 `json:"dist,omitempty"`
	Cad  int     `json:"cad,omitempty"`
}

// LapSummary is one lap.
type LapSummary struct {
	Start    float64 `json:"startSec"`
	Elapsed  float64 `json:"elapsedSec"`
	Distance float64 `json:"distanceM"`
	AvgHR    int     `json:"avgHr"`
	AvgSpeed float64 `json:"avgSpeedMs"`
}

// ActivityDetail is the summary plus track and laps.
type ActivityDetail struct {
	ActivitySummary
	Points []TrackPoint `json:"points"`
	Laps   []LapSummary `json:"laps"`
}

// Activity decodes one activity with its track (downsampled to maxPoints).
func (s *Service) Activity(ctx context.Context, name string, maxPoints int) (*ActivityDetail, error) {
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return nil, errors.New("bad activity name")
	}

	data, err := s.dev.Read(ctx, "GARMIN/Activity/"+name)
	if err != nil {
		return nil, err
	}

	sum, err := s.summarize(data, name)
	if err != nil {
		return nil, err
	}

	f, _ := fit.Decode(data)
	d := &ActivityDetail{ActivitySummary: sum}
	start := float64(fit.Timestamp(sum.Start))
	recs := f.MessagesByNum(20)

	if maxPoints <= 0 {
		maxPoints = 1500
	}

	step := 1
	if len(recs) > maxPoints {
		step = (len(recs) + maxPoints - 1) / maxPoints
	}

	for i := 0; i < len(recs); i += step {
		m := s.profile.DescribeMessage(recs[i])
		d.Points = append(d.Points, trackPoint(&m, start))
	}

	for _, lm := range f.MessagesByNum(19) {
		m := s.profile.DescribeMessage(lm)
		d.Laps = append(d.Laps, lapSummary(&m, start))
	}

	return d, nil
}

// numOr returns the first of the named fields that is present.
func numOr(m *fit.NamedMessage, names ...string) (float64, bool) {
	for _, n := range names {
		if v := fieldNum(m, n); !math.IsNaN(v) {
			return v, true
		}
	}

	return 0, false
}

// trackPoint reads one record message.
func trackPoint(m *fit.NamedMessage, start float64) TrackPoint {
	p := TrackPoint{}
	if ts, ok := numOr(m, "timestamp"); ok {
		p.T = ts - start
	}

	if lat, lon := fieldNum(m, "positionLat"), fieldNum(m, "positionLong"); !math.IsNaN(lat) && !math.IsNaN(lon) {
		p.Lat, p.Lon = semicircles(lat), semicircles(lon)
	}

	if v, ok := numOr(m, "heartRate"); ok {
		p.HR = int(v)
	}

	if v, ok := numOr(m, "enhancedSpeed", "speed"); ok {
		p.Spd = v
	}

	if v, ok := numOr(m, "enhancedAltitude", "altitude"); ok {
		p.Alt = v
	}

	if v, ok := numOr(m, "distance"); ok {
		p.Dist = v
	}

	if v, ok := numOr(m, "cadence"); ok {
		p.Cad = int(v)
	}

	return p
}

// lapSummary reads one lap message.
func lapSummary(m *fit.NamedMessage, start float64) LapSummary {
	l := LapSummary{}
	if v, ok := numOr(m, "startTime"); ok {
		l.Start = v - start
	}

	if v, ok := numOr(m, "totalElapsedTime"); ok {
		l.Elapsed = v
	}

	if v, ok := numOr(m, "totalDistance"); ok {
		l.Distance = v
	}

	if v, ok := numOr(m, "avgHeartRate"); ok {
		l.AvgHR = int(v)
	}

	if v, ok := numOr(m, "enhancedAvgSpeed", "avgSpeed"); ok {
		l.AvgSpeed = v
	}

	return l
}

// ---------- daily health from Garmin Connect ----------

// DailyHealth is one day as Garmin Connect summarises it.
type DailyHealth struct {
	Date             string `json:"date"`
	Steps            int    `json:"steps"`
	StepGoal         int    `json:"stepGoal"`
	DistanceM        int    `json:"distanceM"`
	RestingHR        int    `json:"restingHr"`
	MinHR            int    `json:"minHr"`
	MaxHR            int    `json:"maxHr"`
	AvgStress        int    `json:"avgStress"`
	BodyBatteryHigh  int    `json:"bodyBatteryHigh"`
	BodyBatteryLow   int    `json:"bodyBatteryLow"`
	SleepSec         int    `json:"sleepSec"`
	Calories         int    `json:"calories"`
	ActiveCalories   int    `json:"activeCalories"`
	Floors           int    `json:"floors"`
	IntensityMinutes int    `json:"intensityMinutes"`
	ActiveSec        int    `json:"activeSec"`
	HighlyActiveSec  int    `json:"highlyActiveSec"`
	Error            string `json:"error,omitempty"`
}

func (c *Cloud) displayName(ctx context.Context) (string, error) {
	st := c.Status(ctx)
	if !st.LoggedIn {
		return "", errors.New("not signed in to Garmin Connect")
	}

	if st.Profile == nil || st.Profile.DisplayName == "" {
		return "", errors.New("the Garmin Connect profile has no display name")
	}

	return st.Profile.DisplayName, nil
}

func asInt(m map[string]any, k string) int {
	if v, ok := m[k].(float64); ok {
		return int(math.Round(v))
	}

	return 0
}

// Daily fetches the last n days (today included) from Garmin Connect.
func (c *Cloud) Daily(ctx context.Context, n int) ([]DailyHealth, error) {
	if n <= 0 {
		n = 14
	}

	if n > 90 {
		n = 90
	}

	dn, err := c.displayName(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]DailyHealth, n)

	var wg sync.WaitGroup

	sem := make(chan struct{}, 4)

	for i := range n {
		date := time.Now().AddDate(0, 0, -(n - 1 - i)).Format(time.DateOnly)
		out[i].Date = date

		wg.Go(func() {
			if !acquire(ctx, sem) {
				out[i].Error = ctx.Err().Error()

				return
			}

			defer func() { <-sem }()

			var m map[string]any
			if err := c.gc.GetJSON(ctx, fmt.Sprintf("/usersummary-service/usersummary/daily/%s?calendarDate=%s", dn, date), &m); err != nil {
				out[i].Error = err.Error()
				return
			}

			fillDaily(&out[i], m)
		})
	}

	wg.Wait()

	return out, nil
}

// fillDaily copies a usersummary answer into a row.
func fillDaily(d *DailyHealth, m map[string]any) {
	d.Steps, d.StepGoal, d.DistanceM = asInt(m, "totalSteps"), asInt(m, "dailyStepGoal"), asInt(m, "totalDistanceMeters")
	d.RestingHR, d.MinHR, d.MaxHR = asInt(m, "restingHeartRate"), asInt(m, "minHeartRate"), asInt(m, "maxHeartRate")
	d.AvgStress, d.BodyBatteryHigh, d.BodyBatteryLow = asInt(m, "averageStressLevel"), asInt(m, "bodyBatteryHighestValue"), asInt(m, "bodyBatteryLowestValue")
	d.SleepSec, d.Calories, d.ActiveCalories, d.Floors = asInt(m, "sleepingSeconds"), asInt(m, "totalKilocalories"), asInt(m, "activeKilocalories"), asInt(m, "floorsAscended")
	d.IntensityMinutes = asInt(m, "moderateIntensityMinutes") + 2*asInt(m, "vigorousIntensityMinutes")
	d.ActiveSec, d.HighlyActiveSec = asInt(m, "activeSeconds"), asInt(m, "highlyActiveSeconds")
}

// Series is a time series of [unix ms, value] pairs.
type Series [][2]float64

// DayDetail is the intraday data of one day.
type DayDetail struct {
	Date        string        `json:"date"`
	HeartRate   Series        `json:"heartRate"`
	Stress      Series        `json:"stress"`
	BodyBattery Series        `json:"bodyBattery"`
	Sleep       *SleepSummary `json:"sleep,omitempty"`
	Errors      []string      `json:"errors,omitempty"`
}

// SleepSummary is one night.
type SleepSummary struct {
	StartMs  int64  `json:"startMs"`
	EndMs    int64  `json:"endMs"`
	DeepSec  int    `json:"deepSec"`
	LightSec int    `json:"lightSec"`
	RemSec   int    `json:"remSec"`
	AwakeSec int    `json:"awakeSec"`
	Score    int    `json:"score"`
	Quality  string `json:"quality,omitempty"`
	Levels   Series `json:"levels"` // [startMs, level] 0 deep 1 light 2 rem 3 awake
}

func pairs(v any, idxTime, idxVal int) Series {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make(Series, 0, len(arr))
	for _, e := range arr {
		p, ok := e.([]any)
		if !ok || len(p) <= idxTime || len(p) <= idxVal {
			continue
		}

		t, ok1 := p[idxTime].(float64)

		val, ok2 := p[idxVal].(float64)
		if ok1 && ok2 {
			out = append(out, [2]float64{t, val})
		}
	}

	return out
}

// Day fetches heart rate, stress, body battery and sleep of one day.
func (c *Cloud) Day(ctx context.Context, date string) (*DayDetail, error) {
	dn, err := c.displayName(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return nil, errors.New("date must be YYYY-MM-DD")
	}

	d := &DayDetail{Date: date}

	var hr map[string]any
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/wellness-service/wellness/dailyHeartRate/%s?date=%s", dn, date), &hr); err != nil {
		d.Errors = append(d.Errors, "heart rate: "+err.Error())
	} else {
		d.HeartRate = pairs(hr["heartRateValues"], 0, 1)
	}

	var st map[string]any
	if err := c.gc.GetJSON(ctx, "/wellness-service/wellness/dailyStress/"+date, &st); err != nil {
		d.Errors = append(d.Errors, "stress: "+err.Error())
	} else {
		d.Stress = pairs(st["stressValuesArray"], 0, 1)
		d.BodyBattery = pairs(st["bodyBatteryValuesArray"], 0, 2)
	}

	var sl map[string]any
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/wellness-service/wellness/dailySleepData/%s?date=%s&nonSleepBufferMinutes=60", dn, date), &sl); err != nil {
		d.Errors = append(d.Errors, "sleep: "+err.Error())
	} else {
		d.Sleep = sleepSummary(sl)
	}

	return d, nil
}

// sleepSummary reads a dailySleepData answer; nil when no sleep was recorded.
func sleepSummary(sl map[string]any) *SleepSummary {
	dto, ok := sl["dailySleepDTO"].(map[string]any)
	if !ok || asInt(dto, "sleepTimeSeconds") <= 0 {
		return nil
	}

	ss := &SleepSummary{
		StartMs: int64(num(dto["sleepStartTimestampGMT"])), EndMs: int64(num(dto["sleepEndTimestampGMT"])),
		DeepSec: asInt(dto, "deepSleepSeconds"), LightSec: asInt(dto, "lightSleepSeconds"),
		RemSec: asInt(dto, "remSleepSeconds"), AwakeSec: asInt(dto, "awakeSleepSeconds"),
	}

	if sc, ok := dto["sleepScores"].(map[string]any); ok {
		if ov, ok := sc["overall"].(map[string]any); ok {
			ss.Score = asInt(ov, "value")
			ss.Quality, _ = ov["qualifierKey"].(string)
		}
	}

	if lv, ok := sl["sleepLevels"].([]any); ok {
		for _, e := range lv {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}

			t, _ := time.Parse("2006-01-02T15:04:05.0", fmt.Sprint(m["startGMT"]))
			ss.Levels = append(ss.Levels, [2]float64{float64(t.UnixMilli()), num(m["activityLevel"])})
		}
	}

	return ss
}
