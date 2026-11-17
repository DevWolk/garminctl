package fit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("testdata/*")
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata: %v", err)
	}

	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // testdata fixture
		if err != nil {
			t.Fatal(err)
		}

		if !IsFIT(data) {
			continue
		}

		f, err := Decode(data)
		if err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}

		out := f.Encode()
		if !bytes.Equal(out, data) {
			t.Errorf("%s: re-encoded bytes differ (len %d vs %d)", path, len(out), len(data))
		}
	}
}

func TestDescribeSettings(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/Settings.fit")
	if err != nil {
		t.Fatal(err)
	}

	f, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	p := MustProfile()
	msgs := p.Describe(f)

	var ds *NamedMessage

	for i := range msgs {
		if msgs[i].Name == "deviceSettings" {
			ds = &msgs[i]
		}
	}

	if ds == nil {
		t.Fatal("deviceSettings not found")
	}

	bl, ok := ds.FieldByName("backlightMode")
	if !ok || bl.Value != "autoBrightness" {
		t.Fatalf("backlightMode = %v (ok=%v)", bl, ok)
	}

	steps, _ := ds.FieldByName("autosyncMinSteps")
	if steps.Value != uint64(2000) {
		t.Fatalf("autosyncMinSteps = %v", steps.Value)
	}

	var up *NamedMessage

	for i := range msgs {
		if msgs[i].Name == "userProfile" {
			up = &msgs[i]
		}
	}

	h, _ := up.FieldByName("height")
	if h.Value != 1.7 {
		t.Fatalf("height = %v", h.Value)
	}

	w, _ := up.FieldByName("weight")
	if w.Value != 70.0 {
		t.Fatalf("weight = %v", w.Value)
	}
}

func TestSetValueInPlace(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/Settings.fit")
	if err != nil {
		t.Fatal(err)
	}

	f, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	ds := f.MessagesByNum(2)[0]

	fld, _ := ds.Field(58) // autosync_min_steps
	if err := fld.SetValue(int64(1500), ds.Def.BigEndian); err != nil {
		t.Fatal(err)
	}

	out := f.Encode()

	g, err := Decode(out)
	if err != nil {
		t.Fatal(err)
	}

	if v := g.MessagesByNum(2)[0].Value(58); v != uint64(1500) {
		t.Fatalf("got %v", v)
	}
}

func TestBuilderMatchesGarminLayout(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/c2fc782d-084e-4110-b7e3-eebf9fb072f5.FIT")
	if err != nil {
		t.Fatal(err)
	}

	orig, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	b := NewBuilder(orig.ProfileVersion, true)
	for _, m := range orig.Messages() {
		vals := map[uint8]any{}
		for _, fld := range m.Fields {
			vals[fld.Def.Num] = fld.Value(true)
		}

		if err := b.Write(m.Def.GlobalNum, m.Def.Fields, vals); err != nil {
			t.Fatal(err)
		}
	}

	out := b.Bytes()
	if !bytes.Equal(out, data) {
		t.Fatalf("builder output differs from Garmin file: %d vs %d bytes", len(out), len(data))
	}
}

func TestEncodeScalar_KeepsLargeUnsigned(t *testing.T) {
	t.Parallel()

	const v = uint64(0x8000000000000001)

	dst := make([]byte, 8)
	if err := encodeScalar(BaseUint64z, dst, v, false); err != nil {
		t.Fatal(err)
	}

	if got := decodeScalar(BaseUint64z, dst, false); got != v {
		t.Fatalf("round trip of %#x through encodeScalar = %#v", v, got)
	}
}
