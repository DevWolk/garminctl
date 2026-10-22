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

	"garminctl/internal/fit"
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
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "garminctl", fmt.Sprintf("activities-%d.json", unit))
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

var activityJobs = struct {
	sync.Mutex
	running map[uint64]bool
}{running: map[uint64]bool{}}

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
	cache := activityCache{Entries: map[string]ActivitySummary{}}
	if b, err := os.ReadFile(cp); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
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
	decode := func(ctx context.Context, names []string) {
		for _, n := range names {
			data, err := s.dev.Read(ctx, "GARMIN/Activity/"+n)
			if err != nil {
				continue
			}
			a, err := s.summarize(data, n)
			if err != nil {
				a = ActivitySummary{File: n, Sport: "unreadable", Size: int64(len(data))}
			}
			s.mu.Lock()
			cache.Entries[n] = a
			b, _ := json.MarshalIndent(cache, "", " ")
			_ = os.MkdirAll(filepath.Dir(cp), 0o700)
			_ = os.WriteFile(cp, b, 0o600)
			s.mu.Unlock()
		}
	}
	if len(todo) > 0 {
		if wait {
			decode(ctx, todo)
			return s.Activities(ctx, false)
		}
		activityJobs.Lock()
		if !activityJobs.running[dx.UnitID] {
			activityJobs.running[dx.UnitID] = true
			go func() {
				defer func() {
					activityJobs.Lock()
					activityJobs.running[dx.UnitID] = false
					activityJobs.Unlock()
				}()
				bctx, cancel := context.WithTimeout(context.Background(), time.Hour)
				defer cancel()
				decode(bctx, todo)
			}()
		}
		activityJobs.Unlock()
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
		p := TrackPoint{}
		if ts := fieldNum(&m, "timestamp"); !math.IsNaN(ts) {
			p.T = ts - start
		}
		if lat, lon := fieldNum(&m, "positionLat"), fieldNum(&m, "positionLong"); !math.IsNaN(lat) && !math.IsNaN(lon) {
			p.Lat, p.Lon = semicircles(lat), semicircles(lon)
		}
		if v := fieldNum(&m, "heartRate"); !math.IsNaN(v) {
			p.HR = int(v)
		}
		if v := fieldNum(&m, "enhancedSpeed"); !math.IsNaN(v) {
			p.Spd = v
		} else if v := fieldNum(&m, "speed"); !math.IsNaN(v) {
			p.Spd = v
		}
		if v := fieldNum(&m, "enhancedAltitude"); !math.IsNaN(v) {
			p.Alt = v
		} else if v := fieldNum(&m, "altitude"); !math.IsNaN(v) {
			p.Alt = v
		}
		if v := fieldNum(&m, "distance"); !math.IsNaN(v) {
			p.Dist = v
		}
		if v := fieldNum(&m, "cadence"); !math.IsNaN(v) {
			p.Cad = int(v)
		}
		d.Points = append(d.Points, p)
	}
	for _, lm := range f.MessagesByNum(19) {
		m := s.profile.DescribeMessage(lm)
		l := LapSummary{}
		if v := fieldNum(&m, "startTime"); !math.IsNaN(v) {
			l.Start = v - start
		}
		for _, kv := range []struct {
			n string
			p *float64
		}{{"totalElapsedTime", &l.Elapsed}, {"totalDistance", &l.Distance}} {
			if v := fieldNum(&m, kv.n); !math.IsNaN(v) {
				*kv.p = v
			}
		}
		if v := fieldNum(&m, "avgHeartRate"); !math.IsNaN(v) {
			l.AvgHR = int(v)
		}
		if v := fieldNum(&m, "enhancedAvgSpeed"); !math.IsNaN(v) {
			l.AvgSpeed = v
		} else if v := fieldNum(&m, "avgSpeed"); !math.IsNaN(v) {
			l.AvgSpeed = v
		}
		d.Laps = append(d.Laps, l)
	}
	return d, nil
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
		return "", errors.New("Garmin Connect profile has no display name")
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
	for i := 0; i < n; i++ {
		date := time.Now().AddDate(0, 0, -(n - 1 - i)).Format("2006-01-02")
		out[i].Date = date
		wg.Add(1)
		go func(i int, date string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var m map[string]any
			if err := c.gc.GetJSON(ctx, fmt.Sprintf("/usersummary-service/usersummary/daily/%s?calendarDate=%s", dn, date), &m); err != nil {
				out[i].Error = err.Error()
				return
			}
			d := &out[i]
			d.Steps, d.StepGoal, d.DistanceM = asInt(m, "totalSteps"), asInt(m, "dailyStepGoal"), asInt(m, "totalDistanceMeters")
			d.RestingHR, d.MinHR, d.MaxHR = asInt(m, "restingHeartRate"), asInt(m, "minHeartRate"), asInt(m, "maxHeartRate")
			d.AvgStress, d.BodyBatteryHigh, d.BodyBatteryLow = asInt(m, "averageStressLevel"), asInt(m, "bodyBatteryHighestValue"), asInt(m, "bodyBatteryLowestValue")
			d.SleepSec, d.Calories, d.ActiveCalories, d.Floors = asInt(m, "sleepingSeconds"), asInt(m, "totalKilocalories"), asInt(m, "activeKilocalories"), asInt(m, "floorsAscended")
			d.IntensityMinutes = asInt(m, "moderateIntensityMinutes") + 2*asInt(m, "vigorousIntensityMinutes")
			d.ActiveSec, d.HighlyActiveSec = asInt(m, "activeSeconds"), asInt(m, "highlyActiveSeconds")
		}(i, date)
	}
	wg.Wait()
	return out, nil
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
	if _, err := time.Parse("2006-01-02", date); err != nil {
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
	} else if dto, ok := sl["dailySleepDTO"].(map[string]any); ok && asInt(dto, "sleepTimeSeconds") > 0 {
		ss := &SleepSummary{StartMs: int64(num(dto["sleepStartTimestampGMT"])), EndMs: int64(num(dto["sleepEndTimestampGMT"])), DeepSec: asInt(dto, "deepSleepSeconds"), LightSec: asInt(dto, "lightSleepSeconds"), RemSec: asInt(dto, "remSleepSeconds"), AwakeSec: asInt(dto, "awakeSleepSeconds")}
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
		d.Sleep = ss
	}
	return d, nil
}
