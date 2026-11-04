package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/deviceinfo"
)

// historyAccount serves the five range endpoints for every day of the
// requested window; sleep fails while failSleep is set.
type historyAccount struct {
	failSleep atomic.Bool
}

func (a *historyAccount) GetJSON(_ context.Context, path string, out any) error {
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
		DisplayName: func(context.Context) (string, error) { return "user", nil },
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
