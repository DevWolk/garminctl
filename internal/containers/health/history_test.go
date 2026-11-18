package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/deviceinfo"
)

// historyUser is the display name of the synthetic account.
const historyUser = "user"

// historyAccount serves the five range endpoints for every day of the
// requested window; sleep fails while failSleep is set.
type historyAccount struct {
	failSleep  atomic.Bool
	failBefore string // every range request of a window starting before this date fails
	// syncedThrough, when set, is the last day the watch synced; later days
	// are listed without steps or sleep.
	syncedThrough atomic.Value
}

// historyDate finds the first date of a range request.
var historyDate = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

func (a *historyAccount) GetJSON(_ context.Context, path string, out any) error {
	if from := historyDate.FindString(path); a.failBefore != "" && from != "" && from < a.failBefore {
		return errors.New("window unavailable")
	}

	var body string

	switch {
	case strings.HasPrefix(path, "/usersummary-service/stats/steps/daily/"):
		body = a.rows(path, `{"calendarDate":%q,"totalSteps":100}`, `{"calendarDate":%q,"totalSteps":0}`, "[", "]")
	case strings.HasPrefix(path, "/sleep-service/stats/sleep/daily/"):
		if a.failSleep.Load() {
			return errors.New("sleep unavailable")
		}

		body = a.rows(path, `{"calendarDate":%q,"values":{"totalSleepTimeInSeconds":3600}}`, `{"calendarDate":%q,"values":{}}`, `{"individualStats":[`, "]}")
	case strings.HasPrefix(path, "/userstats-service/"):
		body = "{}"
	default:
		body = "[]"
	}

	return json.Unmarshal([]byte(body), out)
}

func (*historyAccount) SendJSON(context.Context, string, string, any, any) error {
	return errors.New("unexpected write")
}

// rows formats one row per day of the window named by the path's last two
// segments, using empty for the days after syncedThrough.
func (a *historyAccount) rows(path, row, empty, open, closing string) string {
	parts := strings.Split(path, "/")

	from, err := time.Parse(time.DateOnly, parts[len(parts)-2])
	if err != nil {
		return open + closing
	}

	to, err := time.Parse(time.DateOnly, parts[len(parts)-1])
	if err != nil {
		return open + closing
	}

	synced, _ := a.syncedThrough.Load().(string)

	var rows []string

	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		date := d.Format(time.DateOnly)
		if synced != "" && date > synced {
			rows = append(rows, fmt.Sprintf(empty, date))
		} else {
			rows = append(rows, fmt.Sprintf(row, date))
		}
	}

	return open + strings.Join(rows, ",") + closing
}

func TestHistoryDoesNotCacheWindowWithFailedFetch(t *testing.T) {
	t.Parallel()

	account := &historyAccount{}
	account.failSleep.Store(true)

	action := HistoryAction{Access: AccountAccess{
		Client:      account,
		DisplayName: func(context.Context) (string, error) { return historyUser, nil },
		DeviceXML:   func(context.Context) (*deviceinfo.DeviceXML, error) { return nil, errors.New("no watch") },
		CacheDir:    t.TempDir(),
	}}

	// 40 days span two windows; the older one ends before the final-day cutoff
	// and would be served entirely from the cache on the next call.
	first, err := action.Run(t.Context(), 40)
	if err != nil {
		t.Fatal(err)
	}

	if first[0].Steps != 100 || first[0].SleepSec != 0 {
		t.Fatalf("first oldest row = %+v, want steps without sleep", first[0])
	}

	account.failSleep.Store(false)

	second, err := action.Run(t.Context(), 40)
	if err != nil {
		t.Fatal(err)
	}

	if second[0].SleepSec != 3600 {
		t.Fatalf("second oldest row = %+v, want sleep refetched after the failed window", second[0])
	}
}

func TestHistoryKeepsPartialRowsAndNamesFailedFields(t *testing.T) {
	t.Parallel()

	account := &historyAccount{}
	account.failSleep.Store(true)

	action := HistoryAction{Access: AccountAccess{
		Client:      account,
		DisplayName: func(context.Context) (string, error) { return historyUser, nil },
		DeviceXML:   func(context.Context) (*deviceinfo.DeviceXML, error) { return nil, errors.New("no watch") },
		CacheDir:    t.TempDir(),
	}}

	rows, err := action.Run(t.Context(), 3)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}

	// The days that did load stay visible; only the failed metric is marked.
	for _, row := range rows {
		if row.Error != "" || row.Steps != 100 {
			t.Fatalf("row %s = %+v, want its loaded steps without an error", row.Date, row)
		}

		if !row.Lacks("sleepSec") || !row.Lacks("calories") || row.Lacks("steps") {
			t.Fatalf("row %s unavailable = %v", row.Date, row.Unavailable)
		}
	}
}

func TestHistoryReportsErrorForDaysWithoutData(t *testing.T) {
	t.Parallel()

	// Only the oldest window starts this far back, even across midnight.
	account := &historyAccount{failBefore: time.Now().AddDate(0, 0, -25).Format(time.DateOnly)}

	action := HistoryAction{Access: AccountAccess{
		Client:      account,
		DisplayName: func(context.Context) (string, error) { return historyUser, nil },
		DeviceXML:   func(context.Context) (*deviceinfo.DeviceXML, error) { return nil, errors.New("no watch") },
		CacheDir:    t.TempDir(),
	}}

	rows, err := action.Run(t.Context(), 40)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 40 {
		t.Fatalf("rows = %d, want 40", len(rows))
	}

	if first := rows[0]; !strings.Contains(first.Error, "window unavailable") || first.Steps != 0 {
		t.Fatalf("first row of the failed window = %+v, want its error", first)
	}

	if last := rows[len(rows)-1]; last.Error != "" || last.Steps != 100 || last.Lacks("steps") {
		t.Fatalf("row of the complete window = %+v", last)
	}
}

func TestHistoryKeepsCachedDaysOfWindowWithFailedFetch(t *testing.T) {
	t.Parallel()

	account := &historyAccount{}
	action := HistoryAction{Access: AccountAccess{
		Client:      account,
		DisplayName: func(context.Context) (string, error) { return historyUser, nil },
		DeviceXML:   func(context.Context) (*deviceinfo.DeviceXML, error) { return nil, errors.New("no watch") },
		CacheDir:    t.TempDir(),
	}}

	if _, err := action.Run(t.Context(), 10); err != nil {
		t.Fatal(err)
	}

	// The window is fetched again for its two newest days, and sleep fails.
	account.failSleep.Store(true)

	rows, err := action.Run(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 10 {
		t.Fatalf("rows = %d, want 10", len(rows))
	}

	for i, row := range rows {
		cached := i < len(rows)-2
		if cached && (row.SleepSec != 3600 || row.Lacks("sleepSec")) {
			t.Fatalf("cached row %s = %+v, want its cached sleep", row.Date, row)
		}

		if !cached && !row.Lacks("sleepSec") {
			t.Fatalf("fetched row %s unavailable = %v, want sleepSec", row.Date, row.Unavailable)
		}
	}
}

func TestHistoryCacheIsPerWatchAndAccount(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, name := range []string{"alice", "bob"} {
		action := HistoryAction{Access: AccountAccess{
			Client:      &historyAccount{},
			DisplayName: func(context.Context) (string, error) { return name, nil },
			DeviceXML:   func(context.Context) (*deviceinfo.DeviceXML, error) { return &deviceinfo.DeviceXML{UnitID: 7}, nil },
			CacheDir:    dir,
		}}

		if _, err := action.Run(t.Context(), 3); err != nil {
			t.Fatal(err)
		}
	}

	files, err := filepath.Glob(filepath.Join(dir, "history-7-*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("cache files %v (%v), want one per account", files, err)
	}
}

func TestHistoryLoadsAndSavesOneCachePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var lookups atomic.Uint64

	// The watch's identity changes between lookups, as it does when it is
	// plugged in during a long fetch.
	action := HistoryAction{Access: AccountAccess{
		Client:      &historyAccount{},
		DisplayName: func(context.Context) (string, error) { return historyUser, nil },
		DeviceXML: func(context.Context) (*deviceinfo.DeviceXML, error) {
			return &deviceinfo.DeviceXML{UnitID: lookups.Add(1)}, nil
		},
		CacheDir: dir,
	}}

	if _, err := action.Run(t.Context(), 3); err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil || len(files) != 1 || !strings.HasPrefix(filepath.Base(files[0]), "history-1-") {
		t.Fatalf("cache files %v (%v), want only the path the call loaded", files, err)
	}
}

func TestHistoryMarksValuesConnectDidNotReport(t *testing.T) {
	t.Parallel()

	action := HistoryAction{Access: AccountAccess{
		Client:      &historyAccount{},
		DisplayName: func(context.Context) (string, error) { return historyUser, nil },
		DeviceXML:   func(context.Context) (*deviceinfo.DeviceXML, error) { return nil, errors.New("no watch") },
		CacheDir:    t.TempDir(),
	}}

	rows, err := action.Run(t.Context(), 3)
	if err != nil {
		t.Fatal(err)
	}

	// The synthetic account reports steps and sleep only; the other range
	// endpoints answer without rows, so their fields read as missing, not zero.
	for _, row := range rows {
		if row.Lacks("steps") || row.Lacks("sleepSec") {
			t.Fatalf("row %s marks a reported value unavailable: %v", row.Date, row.Unavailable)
		}

		for _, field := range []string{"restingHr", "avgStress", "bodyBatteryLow", "bodyBatteryHigh", "stepGoal"} {
			if !row.Lacks(field) {
				t.Fatalf("row %s reports %s although Connect returned none: %v", row.Date, field, row.Unavailable)
			}
		}
	}
}

func TestHistoryRefetchesDaysTheWatchHadNotSynced(t *testing.T) {
	t.Parallel()

	account := &historyAccount{}
	account.syncedThrough.Store(time.Now().AddDate(0, 0, -5).Format(time.DateOnly))

	action := HistoryAction{Access: AccountAccess{
		Client:      account,
		DisplayName: func(context.Context) (string, error) { return historyUser, nil },
		DeviceXML:   func(context.Context) (*deviceinfo.DeviceXML, error) { return nil, errors.New("no watch") },
		CacheDir:    t.TempDir(),
	}}

	// 30 days make an older window ending at the final-day cutoff; its last
	// days had not been synced yet and must not be cached as final.
	first, err := action.Run(t.Context(), 30)
	if err != nil {
		t.Fatal(err)
	}

	unsynced := len(first) - 3
	if first[unsynced].Steps != 0 || !first[unsynced].Lacks("steps") {
		t.Fatalf("unsynced row = %+v, want steps missing", first[unsynced])
	}

	account.syncedThrough.Store("")

	second, err := action.Run(t.Context(), 30)
	if err != nil {
		t.Fatal(err)
	}

	if second[unsynced].Steps != 100 || second[unsynced].SleepSec != 3600 {
		t.Fatalf("row after the watch synced = %+v, want its steps and sleep", second[unsynced])
	}

	if second[0].Steps != 100 {
		t.Fatalf("oldest row = %+v, want its steps", second[0])
	}
}
