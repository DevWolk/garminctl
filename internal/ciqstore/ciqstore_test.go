package ciqstore

import (
	"os"
	"testing"
)

func loadEasyPlus(t *testing.T) *Schema {
	t.Helper()

	b, err := os.ReadFile("testdata/easyplus-v24-vivoactive5.html")
	if err != nil {
		t.Fatal(err)
	}

	s, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

// shape is the comparable part of a Setting.
type shape struct {
	Type, Default, Min, Max string
	MaxLength               int
	ReadOnly                bool
}

func TestParseEasyPlus(t *testing.T) {
	t.Parallel()

	s := loadEasyPlus(t)
	if len(s.Settings) != 88 {
		t.Fatalf("expected 88 settings, got %d", len(s.Settings))
	}

	tests := []struct {
		name          string
		key           string
		ignoreDefault bool
		expected      shape
	}{
		{name: "dropdown with default", key: "bar1a", expected: shape{Type: "select", Default: "101"}},
		{name: "checked toggle", key: "seconds", expected: shape{Type: "toggle", Default: "true"}},
		{name: "text with maxlength", key: "cust_color1", expected: shape{Type: "text", Default: "FF0000", MaxLength: 6}},
		{name: "number with range", key: "metrics_scale", ignoreDefault: true, expected: shape{Type: "number", Min: "0.6", Max: "1.7"}},
		{name: "read-only control", key: "screen2_settings", ignoreDefault: true, expected: shape{Type: "text", ReadOnly: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := s.Get(tt.key)
			if !ok {
				t.Fatalf("%s not found", tt.key)
			}

			if got.Label == "" {
				t.Fatalf("%s has no label", tt.key)
			}

			flat := shape{Type: got.Type, Default: got.Default, Min: got.Min, Max: got.Max, MaxLength: got.MaxLength, ReadOnly: got.ReadOnly}
			if tt.ignoreDefault {
				flat.Default = ""
			}

			if flat != tt.expected {
				t.Fatalf("%s = %+v, want %+v", tt.key, flat, tt.expected)
			}
		})
	}
}

func TestSettingOptions(t *testing.T) {
	t.Parallel()

	bar, ok := loadEasyPlus(t).Get("bar1a")
	if !ok || len(bar.Options) < 10 {
		t.Fatalf("bar1a: %+v", bar)
	}

	if bar.OptionLabel("107") != "Body Battery %" {
		t.Fatalf("bar1a option 107 = %q", bar.OptionLabel("107"))
	}

	if v, err := bar.OptionValue("body battery"); err != nil || v != "107" {
		t.Fatalf("OptionValue(body battery) = %q, %v", v, err)
	}

	if _, err := bar.OptionValue("no such option"); err == nil {
		t.Fatal("expected an error for an unknown option")
	}
}
