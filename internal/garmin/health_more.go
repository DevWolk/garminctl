package garmin

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// History returns one row per day for up to a year from Connect's range
// endpoints (steps, resting heart rate, stress, body battery, sleep), a
// handful of requests instead of one per day. Calories and intensity minutes
// are not available this way and stay zero.
func (c *Cloud) History(ctx context.Context, days int) ([]DailyHealth, error) {
	if days < 1 {
		days = 30
	}
	if days > 366 {
		days = 366
	}
	dn, err := c.displayName(ctx)
	if err != nil {
		return nil, err
	}
	end := time.Now()
	start := end.AddDate(0, 0, -(days - 1))
	byDate := map[string]*DailyHealth{}
	row := func(date string) *DailyHealth {
		if r, ok := byDate[date]; ok {
			return r
		}
		r := &DailyHealth{Date: date}
		byDate[date] = r
		return r
	}
	var errs []string
	// Range endpoints are asked in windows of at most 90 days.
	for ws := start; !ws.After(end); ws = ws.AddDate(0, 0, 90) {
		we := ws.AddDate(0, 0, 89)
		if we.After(end) {
			we = end
		}
		s, e := ws.Format("2006-01-02"), we.Format("2006-01-02")

		var steps []struct {
			CalendarDate  string `json:"calendarDate"`
			TotalSteps    int    `json:"totalSteps"`
			TotalDistance int    `json:"totalDistance"`
			StepGoal      int    `json:"stepGoal"`
		}
		if err := c.gc.GetJSON(ctx, fmt.Sprintf("/usersummary-service/stats/steps/daily/%s/%s", s, e), &steps); err != nil {
			errs = append(errs, "steps: "+err.Error())
		}
		for _, d := range steps {
			r := row(d.CalendarDate)
			r.Steps, r.DistanceM, r.StepGoal = d.TotalSteps, d.TotalDistance, d.StepGoal
		}

		var rhr struct {
			AllMetrics struct {
				MetricsMap map[string][]struct {
					Value        float64 `json:"value"`
					CalendarDate string  `json:"calendarDate"`
				} `json:"metricsMap"`
			} `json:"allMetrics"`
		}
		if err := c.gc.GetJSON(ctx, fmt.Sprintf("/userstats-service/wellness/daily/%s?fromDate=%s&untilDate=%s&metricId=60", dn, s, e), &rhr); err != nil {
			errs = append(errs, "resting heart rate: "+err.Error())
		}
		for _, d := range rhr.AllMetrics.MetricsMap["WELLNESS_RESTING_HEART_RATE"] {
			row(d.CalendarDate).RestingHR = int(d.Value)
		}

		var stress []struct {
			CalendarDate string `json:"calendarDate"`
			Values       struct {
				OverallStressLevel int `json:"overallStressLevel"`
			} `json:"values"`
		}
		if err := c.gc.GetJSON(ctx, fmt.Sprintf("/usersummary-service/stats/stress/daily/%s/%s", s, e), &stress); err != nil {
			errs = append(errs, "stress: "+err.Error())
		}
		for _, d := range stress {
			row(d.CalendarDate).AvgStress = d.Values.OverallStressLevel
		}

		var bb []struct {
			Date   string      `json:"date"`
			Values [][]float64 `json:"bodyBatteryValuesArray"`
		}
		if err := c.gc.GetJSON(ctx, fmt.Sprintf("/wellness-service/wellness/bodyBattery/reports/daily?startDate=%s&endDate=%s", s, e), &bb); err != nil {
			errs = append(errs, "body battery: "+err.Error())
		}
		for _, d := range bb {
			r := row(d.Date)
			lo, hi := 0, 0
			for i, p := range d.Values {
				if len(p) < 2 {
					continue
				}
				v := int(p[1])
				if i == 0 || v < lo {
					lo = v
				}
				if v > hi {
					hi = v
				}
			}
			r.BodyBatteryLow, r.BodyBatteryHigh = lo, hi
		}

		var sl struct {
			IndividualStats []struct {
				CalendarDate string `json:"calendarDate"`
				Values       struct {
					TotalSleepTimeInSeconds int `json:"totalSleepTimeInSeconds"`
				} `json:"values"`
			} `json:"individualStats"`
		}
		if err := c.gc.GetJSON(ctx, fmt.Sprintf("/sleep-service/stats/sleep/daily/%s/%s", s, e), &sl); err != nil {
			errs = append(errs, "sleep: "+err.Error())
		}
		for _, d := range sl.IndividualStats {
			row(d.CalendarDate).SleepSec = d.Values.TotalSleepTimeInSeconds
		}
	}
	out := make([]DailyHealth, 0, days)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		date := d.Format("2006-01-02")
		if r, ok := byDate[date]; ok {
			out = append(out, *r)
		} else {
			out = append(out, DailyHealth{Date: date})
		}
	}
	if len(byDate) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return out, nil
}

// HealthExtras is the rest of what Garmin Connect knows: fitness metrics,
// personal records, badges and weight.
type HealthExtras struct {
	VO2Max         float64          `json:"vo2max,omitempty"`
	VO2MaxDate     string           `json:"vo2maxDate,omitempty"`
	FitnessAge     float64          `json:"fitnessAge,omitempty"`
	ChronologicalA int              `json:"chronologicalAge,omitempty"`
	HRV            *HRVStatus       `json:"hrv,omitempty"`
	Records        []PersonalRecord `json:"records"`
	Badges         []Badge          `json:"badges"`
	Weight         []WeightEntry    `json:"weight"`
	Errors         []string         `json:"errors,omitempty"`
}

// HRVStatus is the nightly heart-rate-variability summary.
type HRVStatus struct {
	Date      string `json:"date"`
	WeeklyAvg int    `json:"weeklyAvg"`
	LastNight int    `json:"lastNight"`
	Status    string `json:"status"`
}

// PersonalRecord is one of Connect's personal records.
type PersonalRecord struct {
	TypeID       int     `json:"typeId"`
	Label        string  `json:"label"`
	Value        float64 `json:"value"`
	Display      string  `json:"display"`
	Date         string  `json:"date"`
	ActivityID   int64   `json:"activityId,omitempty"`
	ActivityType string  `json:"activityType,omitempty"`
}

// Badge is an earned Connect badge.
type Badge struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	Date   string `json:"date"`
	Points int    `json:"points"`
	Count  int    `json:"count"`
}

// WeightEntry is one weigh-in.
type WeightEntry struct {
	Date    string  `json:"date"`
	Kg      float64 `json:"kg"`
	BMI     float64 `json:"bmi,omitempty"`
	BodyFat float64 `json:"bodyFat,omitempty"`
	Source  string  `json:"source,omitempty"`
}

// Record type ids as Connect numbers them; the ones seen on this account are
// labelled, the rest show their number.
var recordLabels = map[int]string{
	1: "1 km run", 2: "1 mile run", 3: "5 km run", 4: "10 km run", 5: "Half marathon", 6: "Marathon",
	7: "Longest run", 8: "Longest ride", 12: "Most steps in a day", 13: "Most steps in a week", 14: "Most steps in a month", 15: "Longest goal streak",
}

func formatRecord(typeID int, v float64) string {
	switch {
	case typeID >= 1 && typeID <= 6:
		return fmtDur(v)
	case typeID == 7 || typeID == 8:
		return fmt.Sprintf("%.2f km", v/1000)
	case typeID >= 12 && typeID <= 14:
		return fmt.Sprintf("%d steps", int(v))
	case typeID == 15:
		return fmt.Sprintf("%d days", int(v))
	}
	return fmt.Sprintf("%g", v)
}

func fmtDur(sec float64) string {
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Extras fetches fitness metrics, records, badges and weight history.
func (c *Cloud) Extras(ctx context.Context) (*HealthExtras, error) {
	dn, err := c.displayName(ctx)
	if err != nil {
		return nil, err
	}
	x := &HealthExtras{Records: []PersonalRecord{}, Badges: []Badge{}, Weight: []WeightEntry{}}
	today := time.Now().Format("2006-01-02")

	var mm []struct {
		Generic *struct {
			CalendarDate string  `json:"calendarDate"`
			VO2Max       float64 `json:"vo2MaxPreciseValue"`
		} `json:"generic"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/metrics-service/metrics/maxmet/daily/%s/%s", time.Now().AddDate(0, 0, -60).Format("2006-01-02"), today), &mm); err != nil {
		x.Errors = append(x.Errors, "VO2 max: "+err.Error())
	}
	for _, m := range mm {
		if m.Generic != nil && m.Generic.VO2Max > 0 {
			x.VO2Max, x.VO2MaxDate = m.Generic.VO2Max, m.Generic.CalendarDate
		}
	}

	var fa struct {
		ChronologicalAge int     `json:"chronologicalAge"`
		FitnessAge       float64 `json:"fitnessAge"`
	}
	if err := c.gc.GetJSON(ctx, "/fitnessage-service/fitnessage/"+today, &fa); err != nil {
		x.Errors = append(x.Errors, "fitness age: "+err.Error())
	} else {
		x.FitnessAge, x.ChronologicalA = fa.FitnessAge, fa.ChronologicalAge
	}

	var hrv struct {
		Summary *struct {
			CalendarDate string `json:"calendarDate"`
			WeeklyAvg    int    `json:"weeklyAvg"`
			LastNightAvg int    `json:"lastNightAvg"`
			Status       string `json:"status"`
		} `json:"hrvSummary"`
	}
	if err := c.gc.GetJSON(ctx, "/hrv-service/hrv/"+today, &hrv); err != nil {
		x.Errors = append(x.Errors, "HRV: "+err.Error())
	} else if hrv.Summary != nil {
		x.HRV = &HRVStatus{Date: hrv.Summary.CalendarDate, WeeklyAvg: hrv.Summary.WeeklyAvg, LastNight: hrv.Summary.LastNightAvg, Status: hrv.Summary.Status}
	}

	var prs []struct {
		TypeID       int     `json:"typeId"`
		Value        float64 `json:"value"`
		ActivityID   int64   `json:"activityId"`
		ActivityType string  `json:"activityType"`
		When         string  `json:"prStartTimeGmtFormatted"`
		ActStart     string  `json:"actStartDateTimeInGMTFormatted"`
	}
	if err := c.gc.GetJSON(ctx, "/personalrecord-service/personalrecord/prs/"+dn, &prs); err != nil {
		x.Errors = append(x.Errors, "records: "+err.Error())
	}
	for _, p := range prs {
		if p.Value == 0 {
			continue
		}
		label, ok := recordLabels[p.TypeID]
		if !ok {
			label = fmt.Sprintf("Record type %d", p.TypeID)
			if p.ActivityType != "" {
				label = fmt.Sprintf("%s record (type %d)", strings.Title(p.ActivityType), p.TypeID)
			}
		}
		when := p.When
		if when == "" {
			when = p.ActStart
		}
		if len(when) >= 10 {
			when = when[:10]
		}
		x.Records = append(x.Records, PersonalRecord{TypeID: p.TypeID, Label: label, Value: p.Value, Display: formatRecord(p.TypeID, p.Value), Date: when, ActivityID: p.ActivityID, ActivityType: p.ActivityType})
	}
	sort.Slice(x.Records, func(i, j int) bool { return x.Records[i].TypeID < x.Records[j].TypeID })

	var badges []struct {
		Name   string `json:"badgeName"`
		Key    string `json:"badgeKey"`
		Earned string `json:"badgeEarnedDate"`
		Points int    `json:"badgePoints"`
		Number int    `json:"badgeEarnedNumber"`
	}
	if err := c.gc.GetJSON(ctx, "/badge-service/badge/earned", &badges); err != nil {
		x.Errors = append(x.Errors, "badges: "+err.Error())
	}
	for _, b := range badges {
		d := b.Earned
		if len(d) >= 10 {
			d = d[:10]
		}
		x.Badges = append(x.Badges, Badge{Name: b.Name, Key: b.Key, Date: d, Points: b.Points, Count: b.Number})
	}
	sort.Slice(x.Badges, func(i, j int) bool { return x.Badges[i].Date > x.Badges[j].Date })

	var wr struct {
		List []struct {
			CalendarDate string  `json:"calendarDate"`
			Weight       float64 `json:"weight"`
			BMI          float64 `json:"bmi"`
			BodyFat      float64 `json:"bodyFat"`
			SourceType   string  `json:"sourceType"`
		} `json:"dateWeightList"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/weight-service/weight/dateRange?startDate=%s&endDate=%s", time.Now().AddDate(-1, 0, 0).Format("2006-01-02"), today), &wr); err != nil {
		x.Errors = append(x.Errors, "weight: "+err.Error())
	}
	for _, w := range wr.List {
		kg := w.Weight
		if kg > 1000 { // grams
			kg /= 1000
		}
		x.Weight = append(x.Weight, WeightEntry{Date: w.CalendarDate, Kg: kg, BMI: w.BMI, BodyFat: w.BodyFat, Source: w.SourceType})
	}
	sort.Slice(x.Weight, func(i, j int) bool { return x.Weight[i].Date < x.Weight[j].Date })
	return x, nil
}

// LogWeight records a manual weigh-in in Garmin Connect (kg).
func (c *Cloud) LogWeight(ctx context.Context, kg float64, when time.Time) error {
	if kg < 20 || kg > 400 {
		return fmt.Errorf("weight %.1f kg is out of range", kg)
	}
	if when.IsZero() {
		when = time.Now()
	}
	body := map[string]any{
		"dateTimestamp": when.Format("2006-01-02T15:04:05.00"),
		"gmtTimestamp":  when.UTC().Format("2006-01-02T15:04:05.00"),
		"unitKey":       "kg",
		"sourceType":    "MANUAL",
		"value":         kg,
	}
	return c.gc.SendJSON(ctx, http.MethodPost, "/weight-service/user-weight", body, nil)
}
