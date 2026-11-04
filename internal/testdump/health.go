package testdump

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/gc"
)

// HealthActivity is the generated synthetic activity in HealthRoot.
const HealthActivity = "fixture.fit"

// HealthRoot adds a synthetic activity and unreadable/ignored files.
func HealthRoot(tb testing.TB) string {
	tb.Helper()
	root := Root(tb)
	AddHealth(tb, root)

	return root
}

// AddHealth adds fixed activity inputs to an existing temporary fixture root.
func AddHealth(tb testing.TB, root string) {
	tb.Helper()

	stamp := time.Date(2000, time.January, 2, 3, 4, 5, 0, time.UTC)
	files := map[string][]byte{
		HealthActivity: healthActivityPayload(tb),
		"invalid.FIT":  []byte("synthetic invalid FIT"),
		"ignored.txt":  []byte("synthetic non-activity file"),
	}

	for name, content := range files {
		file := filepath.Join(root, "GARMIN", "Activity", name)
		if err := os.WriteFile(file, content, 0o600); err != nil {
			tb.Fatal(err)
		}

		if err := os.Chtimes(file, stamp, stamp); err != nil {
			tb.Fatal(err)
		}
	}
}

func healthActivityPayload(tb testing.TB) []byte {
	tb.Helper()

	b := fit.NewBuilder(21217, true)
	start := fit.Timestamp(time.Date(2000, time.January, 2, 12, 0, 0, 0, time.UTC))

	healthWrite(tb, b, 0, []fit.FieldDef{
		{Num: 0, Size: 1, BaseType: fit.BaseEnum},
		{Num: 1, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 2, BaseType: fit.BaseUint16},
		{Num: 3, Size: 4, BaseType: fit.BaseUint32z},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
	}, map[uint8]any{0: 4, 1: 1, 2: 1, 3: 4242, 4: start})
	healthActivityRecords(tb, b, start)
	healthActivityLap(tb, b, start)
	healthActivitySession(tb, b, start)

	return b.Bytes()
}

func healthWrite(tb testing.TB, b *fit.Builder, number uint16, fields []fit.FieldDef, values map[uint8]any) {
	tb.Helper()

	if err := b.Write(number, fields, values); err != nil {
		tb.Fatal(err)
	}
}

func healthActivityRecords(tb testing.TB, b *fit.Builder, start uint32) {
	tb.Helper()

	fields := []fit.FieldDef{
		{Num: 253, Size: 4, BaseType: fit.BaseUint32},
		{Num: 0, Size: 4, BaseType: fit.BaseSint32},
		{Num: 1, Size: 4, BaseType: fit.BaseSint32},
		{Num: 2, Size: 2, BaseType: fit.BaseUint16},
		{Num: 3, Size: 1, BaseType: fit.BaseUint8},
		{Num: 4, Size: 1, BaseType: fit.BaseUint8},
		{Num: 5, Size: 4, BaseType: fit.BaseUint32},
		{Num: 6, Size: 2, BaseType: fit.BaseUint16},
		{Num: 73, Size: 4, BaseType: fit.BaseUint32},
		{Num: 78, Size: 4, BaseType: fit.BaseUint32},
	}

	for i := range 6 {
		values := map[uint8]any{
			253: uint64(start) + uint64(i*10),
			0:   600_000_000 + i*1000,
			1:   200_000_000 + i*1000,
			2:   3000 + i*5,
			3:   100 + i*4,
			4:   80 + i,
			5:   i * 3000,
			6:   2800 + i*10,
		}
		if i%2 == 0 {
			values[73], values[78] = 3000+i*10, 3050+i*5
		}

		healthWrite(tb, b, 20, fields, values)
	}
}

func healthActivityLap(tb testing.TB, b *fit.Builder, start uint32) {
	tb.Helper()
	healthWrite(tb, b, 19, []fit.FieldDef{
		{Num: 253, Size: 4, BaseType: fit.BaseUint32},
		{Num: 2, Size: 4, BaseType: fit.BaseUint32},
		{Num: 7, Size: 4, BaseType: fit.BaseUint32},
		{Num: 9, Size: 4, BaseType: fit.BaseUint32},
		{Num: 13, Size: 2, BaseType: fit.BaseUint16},
		{Num: 15, Size: 1, BaseType: fit.BaseUint8},
		{Num: 110, Size: 4, BaseType: fit.BaseUint32},
	}, map[uint8]any{253: uint64(start) + 60, 2: start, 7: 60_000, 9: 18_000, 13: 2800, 15: 110, 110: 3000})
}

func healthActivitySession(tb testing.TB, b *fit.Builder, start uint32) {
	tb.Helper()
	healthWrite(tb, b, 18, []fit.FieldDef{
		{Num: 253, Size: 4, BaseType: fit.BaseUint32},
		{Num: 2, Size: 4, BaseType: fit.BaseUint32},
		{Num: 3, Size: 4, BaseType: fit.BaseSint32},
		{Num: 4, Size: 4, BaseType: fit.BaseSint32},
		{Num: 5, Size: 1, BaseType: fit.BaseEnum},
		{Num: 6, Size: 1, BaseType: fit.BaseEnum},
		{Num: 7, Size: 4, BaseType: fit.BaseUint32},
		{Num: 8, Size: 4, BaseType: fit.BaseUint32},
		{Num: 9, Size: 4, BaseType: fit.BaseUint32},
		{Num: 11, Size: 2, BaseType: fit.BaseUint16},
		{Num: 14, Size: 2, BaseType: fit.BaseUint16},
		{Num: 16, Size: 1, BaseType: fit.BaseUint8},
		{Num: 17, Size: 1, BaseType: fit.BaseUint8},
		{Num: 18, Size: 1, BaseType: fit.BaseUint8},
		{Num: 22, Size: 2, BaseType: fit.BaseUint16},
		{Num: 23, Size: 2, BaseType: fit.BaseUint16},
		{Num: 124, Size: 4, BaseType: fit.BaseUint32},
	}, map[uint8]any{
		253: uint64(start) + 60,
		2:   start,
		3:   600_000_000,
		4:   200_000_000,
		5:   1,
		6:   0,
		7:   60_000,
		8:   55_000,
		9:   18_000,
		11:  42,
		14:  2800,
		16:  110,
		17:  120,
		18:  80,
		22:  12,
		23:  7,
		124: 3000,
	})
}

// HealthClient supplies synthetic saved authentication and rejects unlisted HTTP.
// Modes are success (also the default), empty, daily error, history error,
// partial, partial day, partial extras, and missing profile.
func HealthClient(tb testing.TB, mode string) *gc.Client {
	tb.Helper()
	client := workoutClient(tb, filepath.Join(tb.TempDir(), "account.json"), "")
	client.HTTP.Transport = healthTransport{mode: mode}

	return client
}

// HealthCLIEnvironment isolates the CLI account, caches and default transport.
// Tests using this helper must not run in parallel.
func HealthCLIEnvironment(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())

	store := gc.DefaultStorePath()
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}

	client := workoutClient(t, store, "")
	client.HTTP.Transport = healthTransport{mode: mode}
	previous := http.DefaultTransport
	http.DefaultTransport = client.HTTP.Transport

	t.Cleanup(func() { http.DefaultTransport = previous })
}

type healthTransport struct{ mode string }

type healthEndpoint struct {
	method string
	path   string
	kind   string
	body   string
	empty  string
}

func (f healthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "connectapi.garmin.com" {
		return nil, fmt.Errorf("unexpected health fixture host: %s", req.URL)
	}

	for _, endpoint := range healthEndpoints() {
		if endpoint.method != req.Method || !healthPathMatches(endpoint.path, req.URL.Path) {
			continue
		}

		status, body, err := f.response(req, endpoint)
		if err != nil {
			return nil, err
		}

		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	}

	return nil, fmt.Errorf("unexpected health fixture request: %s %s", req.Method, req.URL)
}

func healthPathMatches(pattern, actual string) bool {
	want, got := strings.Split(pattern, "/"), strings.Split(actual, "/")
	if len(want) != len(got) {
		return false
	}

	for i, part := range want {
		if part != "{date}" {
			if part != got[i] {
				return false
			}

			continue
		}

		if _, err := time.Parse(time.DateOnly, got[i]); err != nil {
			return false
		}
	}

	return true
}

func (f healthTransport) response(req *http.Request, endpoint healthEndpoint) (int, string, error) {
	if healthFails(f.mode, endpoint.kind) {
		return http.StatusServiceUnavailable, "synthetic health unavailable", nil
	}

	if f.mode == "missing profile" && endpoint.kind == "profile" {
		return http.StatusOK, `{}`, nil
	}

	if f.mode == "empty" {
		return http.StatusOK, endpoint.empty, nil
	}

	if strings.HasPrefix(endpoint.kind, "history ") {
		body, err := healthRangeBody(req, endpoint.kind)
		return http.StatusOK, body, err
	}

	return http.StatusOK, endpoint.body, nil
}

func healthFails(mode, kind string) bool {
	switch mode {
	case "daily error":
		return kind == "daily"
	case "history error":
		return strings.HasPrefix(kind, "history ")
	case "partial":
		return healthFails("partial day", kind) || healthFails("partial extras", kind)
	case "partial day":
		return kind == "day stress"
	case "partial extras":
		return kind == "vo2" || kind == "records"
	default:
		return false
	}
}

func healthEndpoints() []healthEndpoint {
	return []healthEndpoint{
		{http.MethodGet, "/userprofile-service/socialProfile", "profile", `{"displayName":"fixture-health","fullName":"Fixture Person"}`, `{"displayName":"fixture-health","fullName":"Fixture Person"}`},
		{http.MethodGet, "/usersummary-service/usersummary/daily/fixture-health", "daily", healthDailyJSON, `{}`},
		{http.MethodGet, "/usersummary-service/stats/steps/daily/{date}/{date}", "history steps", "", `[]`},
		{http.MethodGet, "/userstats-service/wellness/daily/fixture-health", "history resting", "", `{"allMetrics":{"metricsMap":{}}}`},
		{http.MethodGet, "/usersummary-service/stats/stress/daily/{date}/{date}", "history stress", "", `[]`},
		{http.MethodGet, "/wellness-service/wellness/bodyBattery/reports/daily", "history battery", "", `[]`},
		{http.MethodGet, "/sleep-service/stats/sleep/daily/{date}/{date}", "history sleep", "", `{"individualStats":[]}`},
		{http.MethodGet, "/wellness-service/wellness/dailyHeartRate/fixture-health", "day heart", `{"heartRateValues":[[946771200000,60],[946771260000,62],[1],null,[2,"invalid"]]}`, `{"heartRateValues":[]}`},
		{http.MethodGet, "/wellness-service/wellness/dailyStress/{date}", "day stress", `{"stressValuesArray":[[946771200000,15],[946771260000,20]],"bodyBatteryValuesArray":[[946771200000,0,80],[946771260000,0,75]]}`, `{"stressValuesArray":[],"bodyBatteryValuesArray":[]}`},
		{http.MethodGet, "/wellness-service/wellness/dailySleepData/fixture-health", "day sleep", healthSleepJSON, `{}`},
		{http.MethodGet, "/metrics-service/metrics/maxmet/daily/{date}/{date}", "vo2", `[{"generic":null},{"generic":{"calendarDate":"2000-01-01","vo2MaxPreciseValue":48.1}},{"generic":{"calendarDate":"2000-01-02","vo2MaxPreciseValue":49.25}}]`, `[]`},
		{http.MethodGet, "/fitnessage-service/fitnessage/{date}", "fitness age", `{"chronologicalAge":40,"fitnessAge":35.5}`, `{}`},
		{http.MethodGet, "/hrv-service/hrv/{date}", "hrv", `{"hrvSummary":{"calendarDate":"2000-01-02","weeklyAvg":50,"lastNightAvg":54,"status":"BALANCED"}}`, `{}`},
		{http.MethodGet, "/personalrecord-service/personalrecord/prs/fixture-health", "records", healthRecordsJSON, `[]`},
		{http.MethodGet, "/badge-service/badge/earned", "badges", `[{"badgeName":"Fixture older","badgeKey":"older","badgeEarnedDate":"2000-01-01T12:00:00","badgePoints":1,"badgeEarnedNumber":1},{"badgeName":"Fixture newer","badgeKey":"newer","badgeEarnedDate":"2000-01-02T12:00:00","badgePoints":2,"badgeEarnedNumber":3}]`, `[]`},
		{http.MethodGet, "/weight-service/weight/dateRange", "weight", `{"dateWeightList":[{"calendarDate":"2000-01-02","weight":71.25,"bmi":22.5,"bodyFat":15.5,"sourceType":"MANUAL"},{"calendarDate":"2000-01-01","weight":72000,"sourceType":"SCALE"}]}`, `{"dateWeightList":[]}`},
		{http.MethodPost, "/weight-service/user-weight", "weight write", `{}`, `{}`},
	}
}

const healthDailyJSON = `{"totalSteps":1234.6,"dailyStepGoal":8000,"totalDistanceMeters":901,"restingHeartRate":55,"minHeartRate":44,"maxHeartRate":144,"averageStressLevel":23,"bodyBatteryHighestValue":90,"bodyBatteryLowestValue":25,"sleepingSeconds":25200,"totalKilocalories":2200,"activeKilocalories":450,"floorsAscended":7,"moderateIntensityMinutes":12,"vigorousIntensityMinutes":8,"activeSeconds":3000,"highlyActiveSeconds":600}`

const healthSleepJSON = `{"dailySleepDTO":{"sleepTimeSeconds":25200,"sleepStartTimestampGMT":946764000000,"sleepEndTimestampGMT":946789200000,"deepSleepSeconds":3600,"lightSleepSeconds":14400,"remSleepSeconds":7200,"awakeSleepSeconds":300,"sleepScores":{"overall":{"value":85,"qualifierKey":"GOOD"}}},"sleepLevels":[{"startGMT":"2000-01-01T22:00:00.0","activityLevel":0},{"startGMT":"2000-01-01T23:00:00.0","activityLevel":1},{"startGMT":"2000-01-02T03:00:00.0","activityLevel":2}]}`

const healthRecordsJSON = `[{"typeId":99,"value":12.5,"activityType":"trail_running","actStartDateTimeInGMTFormatted":"2000-01-02T12:00:00"},{"typeId":3,"value":1200.4,"activityId":42,"activityType":"running","prStartTimeGmtFormatted":"2000-01-01T12:00:00"},{"typeId":7,"value":22000,"prStartTimeGmtFormatted":"2000-01-02"},{"typeId":12,"value":15000,"prStartTimeGmtFormatted":"2000-01-02"},{"typeId":15,"value":10,"prStartTimeGmtFormatted":"2000-01-02"},{"typeId":98,"value":7,"prStartTimeGmtFormatted":"2000-01-02"},{"typeId":4,"value":0}]`

func healthRangeBody(req *http.Request, kind string) (string, error) {
	from, to := healthRangeDates(req, kind)

	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return "", fmt.Errorf("health fixture range start: %w", err)
	}

	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return "", fmt.Errorf("health fixture range end: %w", err)
	}

	if end.Before(start) || end.Sub(start) > 27*24*time.Hour {
		return "", fmt.Errorf("health fixture range exceeds 28 days: %s..%s", from, to)
	}

	prefix, row, suffix := healthRangeShape(kind)
	rows := make([]string, 0, 28)

	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		rows = append(rows, fmt.Sprintf(row, date.Format(time.DateOnly)))
	}

	return prefix + strings.Join(rows, ",") + suffix, nil
}

func healthRangeDates(req *http.Request, kind string) (string, string) {
	switch kind {
	case "history resting":
		return req.URL.Query().Get("fromDate"), req.URL.Query().Get("untilDate")
	case "history battery":
		return req.URL.Query().Get("startDate"), req.URL.Query().Get("endDate")
	default:
		parts := strings.Split(req.URL.Path, "/")
		return parts[len(parts)-2], parts[len(parts)-1]
	}
}

func healthRangeShape(kind string) (prefix, row, suffix string) {
	switch kind {
	case "history steps":
		return `[`, `{"calendarDate":%q,"totalSteps":1235,"totalDistance":901,"stepGoal":8000}`, `]`
	case "history resting":
		return `{"allMetrics":{"metricsMap":{"WELLNESS_RESTING_HEART_RATE":[`, `{"calendarDate":%q,"value":55}`, `]}}}`
	case "history stress":
		return `[`, `{"calendarDate":%q,"values":{"overallStressLevel":23}}`, `]`
	case "history battery":
		return `[`, `{"date":%q,"bodyBatteryValuesArray":[[1],[946771200000,90],[946771260000,25]]}`, `]`
	case "history sleep":
		return `{"individualStats":[`, `{"calendarDate":%q,"values":{"totalSleepTimeInSeconds":25200}}`, `]}`
	default:
		return "", "", ""
	}
}
