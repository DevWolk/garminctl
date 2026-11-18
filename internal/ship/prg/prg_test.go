package prg

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/ciq"
	"github.com/DevWolk/garminctl/internal/ship/ciqstore"
)

func readFaceFixture(t *testing.T) []byte {
	t.Helper()

	b, err := os.ReadFile(faceFixture)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestSections(t *testing.T) {
	t.Parallel()

	b := readFaceFixture(t)
	want := faceSections(t)
	encodeSections(want)

	got, err := Sections(b)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("sections = %+v, %v; want %+v", got, err, want)
	}

	last := got[len(got)-1]
	if end := last.Offset + 8 + len(last.Data); len(b)-end != 8 {
		t.Fatalf("the closing record is %d bytes after the last section", len(b)-end)
	}

	if _, err := Sections(b[:last.Offset+8]); err == nil || !strings.Contains(err.Error(), "exceeds file") {
		t.Fatalf("truncated section error = %v", err)
	}

	if _, err := Sections(make([]byte, 16)); err == nil {
		t.Fatal("a file of closing records has no sections")
	}
}

func TestDefaultSettings(t *testing.T) {
	t.Parallel()

	b := readFaceFixture(t)

	d, err := DefaultSettings(b)
	if err != nil {
		t.Fatal(err)
	}

	if len(d) != 17 || !reflect.DeepEqual(d, faceDefaults()) {
		t.Fatalf("defaults = %#v", d)
	}

	if v, _ := d.Get("custom_color"); v != "FF0000" {
		t.Fatalf("custom_color default = %v", v)
	}

	withoutSettings := encodeSections([]Section{{Magic: MagicHeader, Data: make([]byte, 4)}})
	if _, err := DefaultSettings(withoutSettings); err == nil || !strings.Contains(err.Error(), "no SETTINGS section") {
		t.Fatalf("missing settings error = %v", err)
	}

	list, err := ciq.Encode([]any{int32(1)})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := DefaultSettings(encodeSections([]Section{{Magic: MagicSettings, Data: list}})); err == nil || !strings.Contains(err.Error(), "not a dictionary") {
		t.Fatalf("non-dictionary settings error = %v", err)
	}
}

func TestStringResources(t *testing.T) {
	t.Parallel()

	strs, err := StringResources(readFaceFixture(t))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(strs, "Layout") {
		t.Fatalf("label 'Layout' not found among %d strings", len(strs))
	}

	// Only the well-formed table entries are strings, in file order.
	if !slices.Equal(strs, faceStrings()) {
		t.Fatalf("strings = %q, want %q", strs, faceStrings())
	}
}

// The binary, the store page and the .SET file describe one synthetic app.
func TestFaceFixturesShareOneSchema(t *testing.T) {
	t.Parallel()

	keys := func(d ciq.Dict) []string {
		out := make([]string, len(d))
		for i, pair := range d {
			out[i], _ = pair.Key.(string)
		}

		return out
	}
	defaults := keys(faceDefaults())

	page, err := os.ReadFile("../ciqstore/testdata/settings-page.html")
	if err != nil {
		t.Fatal(err)
	}

	schema, err := ciqstore.Parse(page)
	if err != nil {
		t.Fatal(err)
	}

	var form []string
	for _, setting := range schema.Settings {
		form = append(form, setting.Key)
	}

	if !slices.Equal(form, defaults) {
		t.Fatalf("store page keys = %v, binary defaults = %v", form, defaults)
	}

	stored, err := os.ReadFile("../ciq/testdata/FACE0001.SET")
	if err != nil {
		t.Fatal(err)
	}

	value, err := ciq.Decode(stored)
	if err != nil {
		t.Fatal(err)
	}

	dict, _ := value.(ciq.Dict)
	watch := keys(dict)

	// The watch stores the same keys in its own order.
	slices.Sort(watch)
	slices.Sort(defaults)

	if !slices.Equal(watch, defaults) {
		t.Fatalf("settings file keys = %v, binary defaults = %v", watch, defaults)
	}
}
