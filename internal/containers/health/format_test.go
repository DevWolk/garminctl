package health

import "testing"

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
