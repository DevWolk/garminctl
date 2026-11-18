package health

import (
	"slices"
	"testing"
)

func TestFillDailyListsValuesTheSummaryLeavesOut(t *testing.T) {
	t.Parallel()

	every := []string{
		"steps", "stepGoal", "distanceM", "restingHr", "minHr", "maxHr", "avgStress", "bodyBatteryHigh", "bodyBatteryLow",
		"sleepSec", "calories", "activeCalories", "floors", "intensityMinutes", "activeSec", "highlyActiveSec",
	}

	tests := []struct {
		name    string
		answer  map[string]any
		lacks   []string
		checked func(DailyHealth) bool
	}{
		{
			name:   "a day Connect has no data for yet answers null for every value",
			answer: map[string]any{"totalSteps": nil, "dailyStepGoal": nil, "restingHeartRate": nil, "calendarDate": "2000-01-02"},
			lacks:  every,
			checked: func(d DailyHealth) bool {
				return d.Steps == 0 && d.StepGoal == 0 && d.BodyBatteryHigh == 0
			},
		},
		{
			name: "reported values are kept and only the missing ones are listed",
			answer: map[string]any{
				"totalSteps": 1200.0, "dailyStepGoal": 7000.0, "totalDistanceMeters": 900.4, "restingHeartRate": 50.0,
				"minHeartRate": 44.0, "maxHeartRate": 120.0, "averageStressLevel": 31.0, "bodyBatteryHighestValue": 80.0,
				"bodyBatteryLowestValue": 12.0, "totalKilocalories": 2100.0, "activeKilocalories": 0.0, "floorsAscended": 0.0,
				"moderateIntensityMinutes": 3.0, "activeSeconds": 600.0, "highlyActiveSeconds": 0.0,
			},
			lacks: []string{"sleepSec"},
			checked: func(d DailyHealth) bool {
				return d.Steps == 1200 && d.DistanceM == 900 && d.ActiveCalories == 0 && d.IntensityMinutes == 3 && d.SleepSec == 0
			},
		},
		{
			name:   "a negative stress level means Connect could not measure it",
			answer: map[string]any{"averageStressLevel": -1.0, "totalSteps": 10.0},
			lacks:  slices.DeleteFunc(slices.Clone(every), func(field string) bool { return field == "steps" }),
			checked: func(d DailyHealth) bool {
				return d.AvgStress == 0 && d.Steps == 10
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var row DailyHealth

			fillDaily(&row, test.answer)

			got := slices.Sorted(slices.Values(row.Unavailable))
			want := slices.Sorted(slices.Values(test.lacks))

			if !slices.Equal(got, want) || !test.checked(row) {
				t.Fatalf("row = %+v\nunavailable = %v\nwant        = %v", row, got, want)
			}
		})
	}
}
