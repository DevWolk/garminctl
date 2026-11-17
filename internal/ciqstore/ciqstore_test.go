package ciqstore

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// settingsPageFixture is a hand-written form for a synthetic watch face.
const settingsPageFixture = "testdata/settings-page.html"

const textControl = "text"

func loadSettingsPage(t *testing.T) *Schema {
	t.Helper()

	b, err := os.ReadFile(settingsPageFixture)
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
	Type, Default, Min, Max, Step string
	MaxLength                     int
	ReadOnly, Required            bool
}

func TestParseSettingsPage(t *testing.T) {
	t.Parallel()

	s := loadSettingsPage(t)

	var keys []string
	for _, st := range s.Settings {
		keys = append(keys, st.Key)
	}

	// Unnamed, hidden and button controls of the page are not settings.
	want := []string{
		"layout", "left_bar", "right_bar", "accent_color", "background_color", "show_seconds", "leading_zero", "second_screen",
		"custom_color", "caption", "api_key", "text_scale", "refresh_minutes", "utc_offset", "section_marker", "units_note", "notes",
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("settings = %v, want %v", keys, want)
	}

	tests := []struct {
		name     string
		key      string
		label    string
		expected shape
	}{
		{name: "dropdown with default", key: "left_bar", label: "Left progress bar", expected: shape{Type: "select", Default: "101"}},
		{name: "dropdown with negative value", key: "accent_color", label: "Accent color", expected: shape{Type: "select", Default: "16711680"}},
		{name: "checked toggle", key: "show_seconds", label: "Show seconds", expected: shape{Type: "toggle", Default: "true"}},
		{name: "unchecked toggle", key: "leading_zero", label: "Leading zero for hours", expected: shape{Type: "toggle", Default: "false"}},
		{name: "text with maxlength", key: "custom_color", label: "Custom color in hex format (RRGGBB)", expected: shape{Type: textControl, Default: "FF0000", MaxLength: 6}},
		{name: "input without a type", key: "api_key", label: "Weather service key", expected: shape{Type: textControl}},
		{name: "number with range", key: "text_scale", label: "Text size scale", expected: shape{Type: "number", Default: "1.0", Min: "0.6", Max: "1.7", Step: "any", Required: true}},
		{name: "number without range", key: "utc_offset", label: "Second time zone offset (hours)", expected: shape{Type: "number", Default: "5.0", Step: "any", Required: true}},
		{name: "read-only control", key: "section_marker", label: "====================", expected: shape{Type: textControl, Default: "=== Second screen ===", ReadOnly: true}},
		{name: "escaped value and wrapped label", key: "units_note", label: "Units are set on the watch", expected: shape{Type: textControl, Default: "Menu->System->Units", ReadOnly: true}},
		{name: "text area", key: "notes", label: "Notes", expected: shape{Type: textControl, Default: "Changes apply after sync.", ReadOnly: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := s.Get(tt.key)
			if !ok {
				t.Fatalf("%s not found", tt.key)
			}

			if got.Label != tt.label {
				t.Fatalf("%s label = %q, want %q", tt.key, got.Label, tt.label)
			}

			flat := shape{
				Type: got.Type, Default: got.Default, Min: got.Min, Max: got.Max, Step: got.Step,
				MaxLength: got.MaxLength, ReadOnly: got.ReadOnly, Required: got.Required,
			}
			if flat != tt.expected {
				t.Fatalf("%s = %+v, want %+v", tt.key, flat, tt.expected)
			}
		})
	}
}

func TestSettingOptions(t *testing.T) {
	t.Parallel()

	bar, ok := loadSettingsPage(t).Get("left_bar")
	if !ok || len(bar.Options) != 12 {
		t.Fatalf("left_bar: %+v", bar)
	}

	if bar.OptionLabel("107") != "Energy reserve %" {
		t.Fatalf("left_bar option 107 = %q", bar.OptionLabel("107"))
	}

	for input, want := range map[string]string{"energy reserve": "107", " STEPS % ": "101", "110": "110"} {
		if v, err := bar.OptionValue(input); err != nil || v != want {
			t.Fatalf("OptionValue(%q) = %q, %v; want %q", input, v, err, want)
		}
	}

	if _, err := bar.OptionValue("no such option"); err == nil {
		t.Fatal("expected an error for an unknown option")
	}

	if _, err := bar.OptionValue("%"); err == nil || !strings.Contains(err.Error(), "matches several options") {
		t.Fatalf("ambiguous option error = %v", err)
	}
}
