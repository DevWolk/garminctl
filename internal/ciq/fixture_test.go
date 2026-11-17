package ciq_test

import (
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/testdump"
)

// settingsFixtures defines the synthetic .SET files in testdata, named after
// the Connect IQ entries of the registry fixture. Rebuild them with
// GARMINCTL_UPDATE_FIXTURES=1 go test ./internal/ciq -run TestSettingsFixturesMatchGenerator.
func settingsFixtures() map[string]ciq.Dict {
	return map[string]ciq.Dict{
		// The watch face of internal/ciqstore/testdata/settings-page.html and
		// internal/prg/testdata/face.prg, in the watch's own key order.
		"FACE0001.SET": {
			{Key: "accent_color", Value: int32(-1)},
			{Key: "refresh_minutes", Value: int32(30)},
			{Key: "show_seconds", Value: true},
			{Key: "api_key", Value: ""},
			{Key: "left_bar", Value: int32(103)},
			{Key: "utc_offset", Value: float32(-3.5)},
			{Key: "custom_color", Value: "3366CC"},
			{Key: "leading_zero", Value: true},
			{Key: "units_note", Value: "Menu->System->Units"},
			{Key: "layout", Value: int32(2)},
			{Key: "second_screen", Value: false},
			{Key: "right_bar", Value: int32(107)},
			{Key: "section_marker", Value: "=== Second screen ==="},
			{Key: "background_color", Value: int32(0)},
			{Key: "caption", Value: "⌚"},
			{Key: "text_scale", Value: float32(1.25)},
			{Key: "notes", Value: "Changes apply after sync."},
		},
		"AUDIO001.SET": {
			{Key: "page_size", Value: int32(5)},
			{Key: "sync_mode", Value: int32(0)},
			{Key: "service_url", Value: "https://media.example.invalid"},
			{Key: "stream_format", Value: "hls"},
			{Key: "auth_url", Value: "https://auth.example.invalid"},
		},
		"Tracker.SET": {
			{Key: "alert_interval", Value: int32(-1)},
		},
	}
}

func TestSettingsFixturesMatchGenerator(t *testing.T) {
	t.Parallel()

	for name, settings := range settingsFixtures() {
		data, err := ciq.Encode(settings)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		testdump.CheckFixture(t, filepath.Join("testdata", name), data)
	}
}
