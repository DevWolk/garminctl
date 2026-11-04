package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// historyWindow is the longest span Connect's range endpoints accept.
const historyWindow = 28

// historyBuild collects the rows of one History call; the fetchers run
// concurrently and write under mu.
type historyBuild struct {
	displayName string

	mu     sync.Mutex
	byDate map[string]*DailyHealth
	errs   []string
}

// row returns the row for date, creating it. The caller holds mu.
func (h *historyBuild) row(date string) *DailyHealth {
	if r, ok := h.byDate[date]; ok {
		return r
	}

	r := &DailyHealth{Date: date}
	h.byDate[date] = r

	return r
}

func (h *historyBuild) fail(what string, err error) {
	h.mu.Lock()
	h.errs = append(h.errs, what+": "+err.Error())
	h.mu.Unlock()
}

// cachedWindow copies the cached rows of ws..we into the build and reports
// whether the whole window was cached. Days after final always need a fetch.
func (h *historyBuild) cachedWindow(cache map[string]DailyHealth, ws, we time.Time, final string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	for d := ws; !d.After(we); d = d.AddDate(0, 0, 1) {
		ds := d.Format(time.DateOnly)

		r, ok := cache[ds]
		if !ok || ds > final {
			return false
		}

		h.byDate[ds] = &r
	}

	return true
}

// rows lays the collected days out in order and refreshes the cache with the
// days that no longer change.
func (h *historyBuild) rows(cache map[string]DailyHealth, start, end time.Time, final string) []DailyHealth {
	out := make([]DailyHealth, 0, len(h.byDate))

	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		date := d.Format(time.DateOnly)

		r, ok := h.byDate[date]
		if !ok {
			out = append(out, DailyHealth{Date: date})
			continue
		}

		out = append(out, *r)
		if date <= final {
			cache[date] = *r
		}
	}

	return out
}

// historyFetch fills the rows of one window (from..to, inclusive dates).
type historyFetch func(ctx context.Context, h *historyBuild, from, to string) error

// History returns one row per day for up to a year from Connect's range
// endpoints (steps, resting heart rate, stress, body battery, sleep). Each of
// them accepts at most 28 days per request, so the span is fetched in 28-day
// windows, a few at a time, and days older than two days are cached on disk.
// Calories and intensity minutes are not available this way and stay zero.
func (c *accountReader) history(ctx context.Context, days int) ([]DailyHealth, error) {
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
	cache := c.loadHistoryCache(ctx)
	final := end.AddDate(0, 0, -2).Format(time.DateOnly) // days up to this one no longer change
	h := &historyBuild{displayName: dn, byDate: map[string]*DailyHealth{}}

	var wg sync.WaitGroup

	sem := make(chan struct{}, 6)

	for ws := start; !ws.After(end); ws = ws.AddDate(0, 0, historyWindow) {
		we := ws.AddDate(0, 0, historyWindow-1)
		if we.After(end) {
			we = end
		}

		if h.cachedWindow(cache, ws, we, final) {
			continue
		}

		c.fetchWindow(ctx, &wg, sem, h, ws.Format(time.DateOnly), we.Format(time.DateOnly))
	}

	wg.Wait()

	out := h.rows(cache, start, end, final)

	if len(h.byDate) == 0 && len(h.errs) > 0 {
		return nil, errors.New(strings.Join(h.errs, "; "))
	}

	c.saveHistoryCache(ctx, cache)

	return out, nil
}

// fetchWindow starts the five range fetches of one window, bounded by sem.
func (c *accountReader) fetchWindow(ctx context.Context, wg *sync.WaitGroup, sem chan struct{}, h *historyBuild, from, to string) {
	fetches := []struct {
		what string
		fn   historyFetch
	}{
		{"steps", c.fetchSteps},
		{"resting heart rate", c.fetchRestingHR},
		{"stress", c.fetchStress},
		{"body battery", c.fetchBodyBattery},
		{"sleep", c.fetchSleep},
	}

	for _, f := range fetches {
		wg.Go(func() {
			if !acquire(ctx, sem) {
				h.fail(f.what, ctx.Err())

				return
			}

			defer func() { <-sem }()

			if err := f.fn(ctx, h, from, to); err != nil {
				h.fail(f.what, err)
			}
		})
	}
}

func (c *accountReader) fetchSteps(ctx context.Context, h *historyBuild, from, to string) error {
	var steps []struct {
		CalendarDate  string `json:"calendarDate"`
		TotalSteps    int    `json:"totalSteps"`
		TotalDistance int    `json:"totalDistance"`
		StepGoal      int    `json:"stepGoal"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/usersummary-service/stats/steps/daily/%s/%s", from, to), &steps); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, d := range steps {
		r := h.row(d.CalendarDate)
		r.Steps, r.DistanceM, r.StepGoal = d.TotalSteps, d.TotalDistance, d.StepGoal
	}

	return nil
}

func (c *accountReader) fetchRestingHR(ctx context.Context, h *historyBuild, from, to string) error {
	var rhr struct {
		AllMetrics struct {
			MetricsMap map[string][]struct {
				Value        float64 `json:"value"`
				CalendarDate string  `json:"calendarDate"`
			} `json:"metricsMap"`
		} `json:"allMetrics"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/userstats-service/wellness/daily/%s?fromDate=%s&untilDate=%s&metricId=60", h.displayName, from, to), &rhr); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, d := range rhr.AllMetrics.MetricsMap["WELLNESS_RESTING_HEART_RATE"] {
		h.row(d.CalendarDate).RestingHR = int(d.Value)
	}

	return nil
}

func (c *accountReader) fetchStress(ctx context.Context, h *historyBuild, from, to string) error {
	var stress []struct {
		CalendarDate string `json:"calendarDate"`
		Values       struct {
			OverallStressLevel int `json:"overallStressLevel"`
		} `json:"values"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/usersummary-service/stats/stress/daily/%s/%s", from, to), &stress); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, d := range stress {
		h.row(d.CalendarDate).AvgStress = d.Values.OverallStressLevel
	}

	return nil
}

func (c *accountReader) fetchBodyBattery(ctx context.Context, h *historyBuild, from, to string) error {
	var bb []struct {
		Date   string      `json:"date"`
		Values [][]float64 `json:"bodyBatteryValuesArray"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/wellness-service/wellness/bodyBattery/reports/daily?startDate=%s&endDate=%s", from, to), &bb); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, d := range bb {
		r := h.row(d.Date)
		r.BodyBatteryLow, r.BodyBatteryHigh = bodyBatteryRange(d.Values)
	}

	return nil
}

// bodyBatteryRange returns the lowest and highest level of a day's samples.
func bodyBatteryRange(values [][]float64) (lo, hi int) {
	first := true

	for _, p := range values {
		if len(p) < 2 {
			continue
		}

		v := int(p[1])
		if first || v < lo {
			lo = v
		}

		if v > hi {
			hi = v
		}

		first = false
	}

	return lo, hi
}

func (c *accountReader) fetchSleep(ctx context.Context, h *historyBuild, from, to string) error {
	var sl struct {
		IndividualStats []struct {
			CalendarDate string `json:"calendarDate"`
			Values       struct {
				TotalSleepTimeInSeconds int `json:"totalSleepTimeInSeconds"`
			} `json:"values"`
		} `json:"individualStats"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/sleep-service/stats/sleep/daily/%s/%s", from, to), &sl); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, d := range sl.IndividualStats {
		h.row(d.CalendarDate).SleepSec = d.Values.TotalSleepTimeInSeconds
	}

	return nil
}

func (c *accountReader) historyCachePath(ctx context.Context) string {
	unit := uint64(0)
	if dx, err := c.deviceXML(ctx); err == nil {
		unit = dx.UnitID
	}

	return filepath.Join(c.cacheDir, fmt.Sprintf("history-%d.json", unit))
}

func (c *accountReader) loadHistoryCache(ctx context.Context) map[string]DailyHealth {
	m := map[string]DailyHealth{}

	b, err := os.ReadFile(c.historyCachePath(ctx))
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}

	return m
}

func (c *accountReader) saveHistoryCache(ctx context.Context, m map[string]DailyHealth) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}

	_ = os.MkdirAll(c.cacheDir, 0o750)
	_ = os.WriteFile(c.historyCachePath(ctx), b, 0o600)
}

// Extras is the rest of what Garmin Connect knows: fitness metrics,
// personal records, badges and weight.
type Extras struct {
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

// Known Connect record type labels; unknown types retain their numeric id.
var recordLabels = map[int]string{
	1: "1 km run", 2: "1 mile run", 3: "5 km run", 4: "10 km run", 5: "Half marathon", 6: "Marathon",
	7: "Longest run", 8: "Longest ride", 12: "Most steps in a day", 13: "Most steps in a week", 14: "Most steps in a month", 15: "Longest goal streak",
}

func formatRecord(typeID int, v float64) string {
	switch {
	case typeID >= 1 && typeID <= 6:
		return FormatDuration(v)
	case typeID == 7 || typeID == 8:
		return fmt.Sprintf("%.2f km", v/1000)
	case typeID >= 12 && typeID <= 14:
		return fmt.Sprintf("%d steps", int(v))
	case typeID == 15:
		return fmt.Sprintf("%d days", int(v))
	}

	return fmt.Sprintf("%g", v)
}

// FormatDuration renders seconds as m:ss or h:mm:ss, rounded to the second.
func FormatDuration(sec float64) string {
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}

	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Extras fetches fitness metrics, records, badges and weight history.
func (c *accountReader) extras(ctx context.Context) (*Extras, error) {
	dn, err := c.displayName(ctx)
	if err != nil {
		return nil, err
	}

	x := &Extras{Records: []PersonalRecord{}, Badges: []Badge{}, Weight: []WeightEntry{}}
	today := time.Now().Format(time.DateOnly)

	c.fetchFitnessMetrics(ctx, x, today)
	c.fetchRecords(ctx, x, dn)
	c.fetchBadges(ctx, x)
	c.fetchWeight(ctx, x, today)

	return x, nil
}

// fetchFitnessMetrics fills VO2 max, fitness age and HRV status.
func (c *accountReader) fetchFitnessMetrics(ctx context.Context, x *Extras, today string) {
	var mm []struct {
		Generic *struct {
			CalendarDate string  `json:"calendarDate"`
			VO2Max       float64 `json:"vo2MaxPreciseValue"`
		} `json:"generic"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/metrics-service/metrics/maxmet/daily/%s/%s", time.Now().AddDate(0, 0, -60).Format(time.DateOnly), today), &mm); err != nil {
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
}

// fetchRecords fills the personal records, sorted by type.
func (c *accountReader) fetchRecords(ctx context.Context, x *Extras, dn string) {
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
				label = fmt.Sprintf("%s record (type %d)", titleCase(p.ActivityType), p.TypeID)
			}
		}

		when := p.When
		if when == "" {
			when = p.ActStart
		}

		x.Records = append(x.Records, PersonalRecord{TypeID: p.TypeID, Label: label, Value: p.Value, Display: formatRecord(p.TypeID, p.Value), Date: datePart(when), ActivityID: p.ActivityID, ActivityType: p.ActivityType})
	}

	sort.Slice(x.Records, func(i, j int) bool { return x.Records[i].TypeID < x.Records[j].TypeID })
}

// fetchBadges fills the earned badges, newest first.
func (c *accountReader) fetchBadges(ctx context.Context, x *Extras) {
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
		x.Badges = append(x.Badges, Badge{Name: b.Name, Key: b.Key, Date: datePart(b.Earned), Points: b.Points, Count: b.Number})
	}

	sort.Slice(x.Badges, func(i, j int) bool { return x.Badges[i].Date > x.Badges[j].Date })
}

// fetchWeight fills a year of weigh-ins, oldest first.
func (c *accountReader) fetchWeight(ctx context.Context, x *Extras, today string) {
	var wr struct {
		List []struct {
			CalendarDate string  `json:"calendarDate"`
			Weight       float64 `json:"weight"`
			BMI          float64 `json:"bmi"`
			BodyFat      float64 `json:"bodyFat"`
			SourceType   string  `json:"sourceType"`
		} `json:"dateWeightList"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/weight-service/weight/dateRange?startDate=%s&endDate=%s", time.Now().AddDate(-1, 0, 0).Format(time.DateOnly), today), &wr); err != nil {
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
}

// datePart keeps the YYYY-MM-DD of a Connect timestamp.
func datePart(ts string) string {
	if len(ts) >= len(time.DateOnly) {
		return ts[:len(time.DateOnly)]
	}

	return ts
}

// LogWeight records a manual weigh-in in Garmin Connect (kg).
func (c *accountReader) logWeight(ctx context.Context, kg float64, when time.Time) error {
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

// titleCase upper-cases the first letter of every word and lower-cases the
// rest; underscores count as word breaks ("trail_running" -> "Trail Running").
func titleCase(s string) string {
	var b strings.Builder

	startWord := true

	for _, r := range strings.ReplaceAll(s, "_", " ") {
		switch {
		case unicode.IsSpace(r):
			startWord = true

			b.WriteRune(r)
		case startWord:
			startWord = false

			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}

	return b.String()
}
