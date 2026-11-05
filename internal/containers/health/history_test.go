package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/deviceinfo"
)

// historyUser is the display name of the synthetic account.
const historyUser = "user"

// historyAccount serves the five range endpoints for every day of the
// requested window; sleep fails while failSleep is set.
type historyAccount struct {
	failSleep atomic.Bool
	failFrom  string // every range request of the window starting here fails
}

func (a *historyAccount) GetJSON(_ context.Context, path string, out any) error {
	if a.failFrom != "" && strings.Contains(path, a.failFrom) {
		return errors.New("window unavailable")
	}

	var body string

	switch {
	case strings.HasPrefix(path, "/usersummary-service/stats/steps/daily/"):
		body = historyRows(path, `{"calendarDate":%q,"totalSteps":100}`, "[", "]")
	case strings.HasPrefix(path, "/sleep-service/stats/sleep/daily/"):
		if a.failSleep.Load() {
			return errors.New("sleep unavailable")
		}

		body = historyRows(path, `{"calendarDate":%q,"values":{"totalSleepTimeInSeconds":3600}}`, `{"individualStats":[`, "]}")
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

// historyRows formats one row per day of the window named by the path's last
// two segments.
func historyRows(path, row, open, closing string) string {
	parts := strings.Split(path, "/")

	from, err := time.Parse(time.DateOnly, parts[len(parts)-2])
	if err != nil {
		return open + closing
	}

	to, err := time.Parse(time.DateOnly, parts[len(parts)-1])
	if err != nil {
		return open + closing
	}

	var rows []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		rows = append(rows, fmt.Sprintf(row, d.Format(time.DateOnly)))
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

	start := time.Now().AddDate(0, 0, -39).Format(time.DateOnly)
	account := &historyAccount{failFrom: start}

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
