package garmin

import (
	"encoding/json"
	"testing"
)

func sampleSpec() *WorkoutSpec {
	var w WorkoutSpec

	_ = json.Unmarshal([]byte(`{"name":"4x400 test","sport":"running","steps":[
	  {"type":"warmup","duration":"10:00"},
	  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},{"type":"recovery","duration":"90s","target":"hr zone 2"}]},
	  {"type":"cooldown","duration":"5m","target":"hr 110-130"}]}`), &w)

	return &w
}

func TestTitleCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "lower word", input: "music", expected: "Music"},
		{name: "upper word", input: "PODCASTS", expected: "Podcasts"},
		{name: "underscores become spaces", input: "trail_running", expected: "Trail Running"},
		{name: "empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := titleCase(tt.input); got != tt.expected {
				t.Fatalf("titleCase(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
