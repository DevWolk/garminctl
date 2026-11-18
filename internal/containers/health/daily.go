package health

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
)

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
	// Unavailable names the fields the source cannot provide; they read zero.
	Unavailable []string `json:"unavailable,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// Lacks reports whether the named JSON field is unavailable in this row.
func (d DailyHealth) Lacks(field string) bool {
	return slices.Contains(d.Unavailable, field)
}

func asInt(m map[string]any, k string) int {
	if v, ok := m[k].(float64); ok {
		return int(math.Round(v))
	}

	return 0
}

// Daily fetches the last n days (today included) from Garmin Connect.
func (c *accountReader) daily(ctx context.Context, n int) ([]DailyHealth, error) {
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

// fillDaily copies a usersummary answer into a row. A value the answer leaves
// out, sets to null or marks as not measured is listed in Unavailable, so a
// day Connect has no data for is not shown as a day of zeros.
func fillDaily(d *DailyHealth, m map[string]any) {
	value := func(field string, keys ...string) int {
		total, found := 0, false

		for i, key := range keys {
			number, ok := m[key].(float64)
			if !ok {
				continue
			}

			found = true
			// Vigorous minutes count twice towards the weekly intensity total.
			total += (i + 1) * int(math.Round(number))
		}

		if !found || total < 0 {
			d.Unavailable = append(d.Unavailable, field)

			return 0
		}

		return total
	}

	d.Steps, d.StepGoal, d.DistanceM = value("steps", "totalSteps"), value("stepGoal", "dailyStepGoal"), value("distanceM", "totalDistanceMeters")
	d.RestingHR, d.MinHR, d.MaxHR = value("restingHr", "restingHeartRate"), value("minHr", "minHeartRate"), value("maxHr", "maxHeartRate")
	d.AvgStress = value("avgStress", "averageStressLevel")
	d.BodyBatteryHigh, d.BodyBatteryLow = value("bodyBatteryHigh", "bodyBatteryHighestValue"), value("bodyBatteryLow", "bodyBatteryLowestValue")
	d.SleepSec, d.Calories, d.ActiveCalories = value("sleepSec", "sleepingSeconds"), value("calories", "totalKilocalories"), value("activeCalories", "activeKilocalories")
	d.Floors = value("floors", "floorsAscended")
	d.IntensityMinutes = value("intensityMinutes", "moderateIntensityMinutes", "vigorousIntensityMinutes")
	d.ActiveSec, d.HighlyActiveSec = value("activeSec", "activeSeconds"), value("highlyActiveSec", "highlyActiveSeconds")
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
func (c *accountReader) day(ctx context.Context, date string) (*DayDetail, error) {
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
