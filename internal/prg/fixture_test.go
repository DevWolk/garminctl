package prg

import (
	"encoding/binary"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/testdump"
)

// faceFixture is the synthetic watch-face binary in testdata. Rebuild it with
// GARMINCTL_UPDATE_FIXTURES=1 go test ./internal/prg -run TestFaceFixtureMatchesGenerator.
const faceFixture = "testdata/face.prg"

// faceDefaults are the property defaults of the fixture app, in the order of
// internal/ciqstore/testdata/settings-page.html.
func faceDefaults() ciq.Dict {
	return ciq.Dict{
		{Key: "layout", Value: int32(1)},
		{Key: "left_bar", Value: int32(101)},
		{Key: "right_bar", Value: int32(102)},
		{Key: "accent_color", Value: int32(16711680)},
		{Key: "background_color", Value: int32(0)},
		{Key: "show_seconds", Value: true},
		{Key: "leading_zero", Value: false},
		{Key: "second_screen", Value: true},
		{Key: "custom_color", Value: "FF0000"},
		{Key: "caption", Value: ""},
		{Key: "api_key", Value: ""},
		{Key: "text_scale", Value: float32(1)},
		{Key: "refresh_minutes", Value: int32(60)},
		{Key: "utc_offset", Value: float32(5)},
		{Key: "section_marker", Value: "=== Second screen ==="},
		{Key: "units_note", Value: "Menu->System->Units"},
		{Key: "notes", Value: "Changes apply after sync."},
	}
}

// faceStrings is the string table of the fixture: the app name, settings
// labels and option names, including multi-line and non-ASCII text.
func faceStrings() []string {
	return []string{
		"Fixture Face", "Layout", "Classic", "Split", "Minimal",
		"Left progress bar", "Right progress bar", "Off", "Steps %", "Battery %", "Energy reserve %",
		"Accent color", "Custom color", "Background color", "Show seconds", "Leading zero for hours",
		"Enable second screen", "Custom color in hex format (RRGGBB)", "Caption text", "Text size scale",
		"Refresh interval (minutes)", "Units are set\non the watch", "Größe\t°C",
	}
}

// faceResources interleaves the string table with bytes the scanner has to
// skip: binary resource data, an entry holding a control character, an empty
// entry and an entry whose length runs past the section.
func faceResources() []byte {
	out := []byte{0x00, 0x04, 0x80, 0x00, 0x00, 0x10, 0x80, 0x00}
	out = append(out, 0x00, 0x01, 0x00, 0x03, 'a', 0x07, 'b')
	out = append(out, 0x00, 0x01, 0x00, 0x00)

	for i, s := range faceStrings() {
		out = append(out, 0x00, 0x01)
		out = binary.BigEndian.AppendUint16(out, uint16(len(s))) //nolint:gosec // fixture labels are far shorter than 64 KiB
		out = append(out, s...)

		if i%4 == 3 {
			out = append(out, 0xFF, 0x80, 0x00, 0x7F)
		}
	}

	return append(out, 0x00, 0x01, 0xFF, 0xFF, 'x')
}

// faceSections lists the sections in the order a compiled app stores them,
// behind the store's leading app-id record. Section bodies other than the
// resources and settings are opaque to this package and hold filler.
func faceSections(t *testing.T) []Section {
	t.Helper()

	settings, err := ciq.Encode(faceDefaults())
	if err != nil {
		t.Fatal(err)
	}

	storeID := []byte{0x5A, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 0xD4}
	filler := func(n int) []byte { return make([]byte, n) }

	return []Section{
		{Magic: 0x0000001D, Data: append([]byte{0, 0, 0, 0x18}, storeID...)},
		{Magic: MagicHeader, Data: filler(16)},
		{Magic: MagicEntryPoints, Data: filler(20)},
		{Magic: MagicData, Data: filler(8)},
		{Magic: MagicCode, Data: filler(8)},
		{Magic: MagicLinkTable, Data: filler(2)},
		{Magic: MagicClassDefs, Data: filler(4)},
		{Magic: MagicResources, Data: faceResources()},
		{Magic: MagicPermissions, Data: filler(2)},
		{Magic: MagicExceptions, Data: filler(2)},
		{Magic: MagicSettings, Data: settings},
		{Magic: MagicDevSig, Data: filler(16)},
		{Magic: MagicStoreSig, Data: filler(16)},
	}
}

// encodeSections writes each section as magic, length and body, then the
// all-zero record that ends the table. It fills in the section offsets.
func encodeSections(sections []Section) []byte {
	var out []byte

	for i := range sections {
		sections[i].Offset = len(out)
		out = binary.BigEndian.AppendUint32(out, sections[i].Magic)
		out = binary.BigEndian.AppendUint32(out, uint32(len(sections[i].Data))) //nolint:gosec // fixture sections are a few hundred bytes
		out = append(out, sections[i].Data...)
	}

	return append(out, make([]byte, 8)...)
}

func TestFaceFixtureMatchesGenerator(t *testing.T) {
	t.Parallel()
	testdump.CheckFixture(t, faceFixture, encodeSections(faceSections(t)))
}
