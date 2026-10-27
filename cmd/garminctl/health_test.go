package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/DevWolk/garminctl/internal/garmin"
)

const (
	dailyRead      = "daily"
	historyRead    = "history"
	healthDaysFlag = "-days"
)

type healthReaderStub struct {
	rows   []garmin.DailyHealth
	err    error
	calls  []string
	days   int
	onRead func(context.Context)
}

func (r *healthReaderStub) Daily(ctx context.Context, days int) ([]garmin.DailyHealth, error) {
	r.calls = append(r.calls, dailyRead)

	r.days = days
	if r.onRead != nil {
		r.onRead(ctx)
	}

	return r.rows, r.err
}

func (r *healthReaderStub) History(ctx context.Context, days int) ([]garmin.DailyHealth, error) {
	r.calls = append(r.calls, historyRead)

	r.days = days
	if r.onRead != nil {
		r.onRead(ctx)
	}

	return r.rows, r.err
}

func TestHealthDailySelectsHistoryForLongRanges(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		args   []string
		days   int
		method string
	}{
		{name: "default", days: 14, method: dailyRead},
		{name: "zero retains daily default", args: []string{healthDaysFlag, "0"}, days: 0, method: dailyRead},
		{name: "negative retains daily default", args: []string{healthDaysFlag, "-1"}, days: -1, method: dailyRead},
		{name: "daily boundary", args: []string{healthDaysFlag, "14"}, days: 14, method: dailyRead},
		{name: "history boundary", args: []string{healthDaysFlag, "15"}, days: 15, method: historyRead},
		{name: "past daily cap", args: []string{healthDaysFlag, "91"}, days: 91, method: historyRead},
		{name: "history owns its cap", args: []string{healthDaysFlag, "367"}, days: 367, method: historyRead},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			reader := &healthReaderStub{onRead: func(got context.Context) {
				if got != ctx {
					t.Error("reader received a different request context")
				}
			}}
			c := &cli{out: newPrinter(&bytes.Buffer{}), errOut: newPrinter(&bytes.Buffer{})}

			if err := c.healthDaily(ctx, reader, tt.args); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(reader.calls, []string{tt.method}) || reader.days != tt.days {
				t.Fatalf("calls=%v days=%d", reader.calls, reader.days)
			}
		})
	}
}

func TestHealthDailyPreservesOutputForBothSources(t *testing.T) {
	t.Parallel()

	rows := []garmin.DailyHealth{
		{Date: "2026-01-02", Steps: 10, StepGoal: 20, RestingHR: 50, AvgStress: 7, BodyBatteryLow: 1, BodyBatteryHigh: 90, SleepSec: 3661, Calories: 123},
		{Date: "2026-01-03", Error: "synthetic day unavailable"},
	}

	const wantText = "date        steps  goal  rHR stress  battery   sleep  kcal\n" +
		"2026-01-02     10    20   50      7   1-90  1:01:01   123\n" +
		"2026-01-03 synthetic day unavailable\n"

	for _, days := range []string{"14", "15"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("days=%s/json=%t", days, asJSON), func(t *testing.T) {
				t.Parallel()

				var out, errOut bytes.Buffer

				c := &cli{asJSON: asJSON, out: newPrinter(&out), errOut: newPrinter(&errOut)}
				if err := c.healthDaily(t.Context(), &healthReaderStub{rows: rows}, []string{healthDaysFlag, days}); err != nil {
					t.Fatal(err)
				}

				if errOut.Len() != 0 {
					t.Fatalf("unexpected diagnostics: %s", &errOut)
				}

				if !asJSON {
					if out.String() != wantText {
						t.Fatalf("text output = %q, want %q", out.String(), wantText)
					}

					return
				}

				var got []garmin.DailyHealth
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatalf("JSON list: %v, output %s", err, &out)
				}

				if !reflect.DeepEqual(got, rows) {
					t.Fatalf("JSON rows = %#v, want %#v", got, rows)
				}
			})
		}
	}
}

func TestHealthDailyErrorsAndEmptyResults(t *testing.T) {
	t.Parallel()

	for _, days := range []string{"14", "15"} {
		for _, tt := range []struct {
			name string
			rows []garmin.DailyHealth
			err  error
			want string
		}{
			{name: "nil", want: "null\n"},
			{name: "empty", rows: []garmin.DailyHealth{}, want: "[]\n"},
			{name: "failure", rows: []garmin.DailyHealth{{Date: "2026-01-04"}}, err: errors.New("synthetic health source failed")},
			{name: "cancelled", err: context.Canceled},
		} {
			t.Run(days+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				var out bytes.Buffer

				c := &cli{asJSON: true, out: newPrinter(&out), errOut: newPrinter(&bytes.Buffer{})}

				err := c.healthDaily(t.Context(), &healthReaderStub{rows: tt.rows, err: tt.err}, []string{healthDaysFlag, days})
				if !errors.Is(err, tt.err) || out.String() != tt.want {
					t.Fatalf("error=%v output=%q, want error=%v output=%q", err, out.String(), tt.err, tt.want)
				}
			})
		}
	}
}

func TestHealthDailyRejectsInvalidDaysBeforeReading(t *testing.T) {
	t.Parallel()

	reader := &healthReaderStub{}
	c := &cli{out: newPrinter(&bytes.Buffer{}), errOut: newPrinter(&bytes.Buffer{})}

	err := c.healthDaily(t.Context(), reader, []string{healthDaysFlag, "invalid"})
	if exitCode(err) != 2 || len(reader.calls) != 0 {
		t.Fatalf("error=%v calls=%v", err, reader.calls)
	}
}
