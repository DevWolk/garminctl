package ciq

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("testdata/*.SET")
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata: %v", err)
	}

	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // testdata fixture
		if err != nil {
			t.Fatal(err)
		}

		v, err := Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		out, err := Encode(v)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(out, data) {
			t.Errorf("%s: re-encoded bytes differ (%d vs %d)", path, len(out), len(data))
		}
	}
}

func TestFixtureValues(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/FACE0001.SET")
	if err != nil {
		t.Fatal(err)
	}

	v, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	d, ok := v.(Dict)
	if !ok || len(d) != 17 {
		t.Fatalf("expected 17-entry dict, got %T len %d", v, len(d))
	}

	if c, _ := d.Get("custom_color"); c != "3366CC" {
		t.Fatalf("custom_color = %v", c)
	}

	if s, _ := d.Get("show_seconds"); s != true {
		t.Fatalf("show_seconds = %v", s)
	}

	if s, _ := d.Get("text_scale"); s != float32(1.25) {
		t.Fatalf("text_scale = %v", s)
	}

	if s, _ := d.Get("left_bar"); s != int32(103) {
		t.Fatalf("left_bar = %v", s)
	}

	if s, _ := d.Get("caption"); s != "⌚" {
		t.Fatalf("caption = %v", s)
	}
}

// The fixtures come from Encode, so this vector written out by hand keeps the
// byte layout of a watch-written file independent of the encoder: sentinels,
// big-endian lengths, NUL-counted string entries, reuse of an interned string
// and every type code.
func TestEncodeWireLayout(t *testing.T) {
	t.Parallel()

	value := Dict{
		{Key: "mode", Value: int32(-1)},
		{Key: "on", Value: true},
		{Key: "name", Value: "mode"},
		{Key: "list", Value: []any{nil, float32(1.5), int64(1) << 40, 0.5, Dict{}}},
	}

	want := slices.Concat(
		[]byte{0xAB, 0xCD, 0xAB, 0xCD, 0, 0, 0, 26},
		[]byte{0, 5, 'm', 'o', 'd', 'e', 0}, // offset 0
		[]byte{0, 3, 'o', 'n', 0},           // offset 7
		[]byte{0, 5, 'n', 'a', 'm', 'e', 0}, // offset 12
		[]byte{0, 5, 'l', 'i', 's', 't', 0}, // offset 19
		[]byte{0xDA, 0x7A, 0xDA, 0x7A, 0, 0, 0, 71},
		[]byte{0x0B, 0, 0, 0, 4},
		[]byte{0x03, 0, 0, 0, 0}, []byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF},
		[]byte{0x03, 0, 0, 0, 7}, []byte{0x09, 1},
		[]byte{0x03, 0, 0, 0, 12}, []byte{0x03, 0, 0, 0, 0},
		[]byte{0x03, 0, 0, 0, 19}, []byte{0x05, 0, 0, 0, 5},
		[]byte{0x00},
		[]byte{0x02, 0x3F, 0xC0, 0, 0},
		[]byte{0x0E, 0, 0, 1, 0, 0, 0, 0, 0},
		[]byte{0x0F, 0x3F, 0xE0, 0, 0, 0, 0, 0, 0},
		[]byte{0x0B, 0, 0, 0, 0},
	)

	got, err := Encode(value)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Encode = %x, %v; want %x", got, err, want)
	}

	back, err := Decode(want)
	if err != nil || !reflect.DeepEqual(back, value) {
		t.Fatalf("Decode = %#v, %v; want %#v", back, err, value)
	}
}

func TestCoerce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		existing any
		input    any
		expected any
		wantErr  bool
	}{
		{name: "int32 from json number", existing: int32(1), input: 14.0, expected: int32(14)},
		{name: "int32 from string", existing: int32(1), input: " 7 ", expected: int32(7)},
		{name: "int32 from bool", existing: int32(0), input: true, expected: int32(1)},
		{name: "int32 rejects a fraction", existing: int32(1), input: 1.5, wantErr: true},
		{name: "int32 rejects overflow", existing: int32(1), input: int64(1) << 40, wantErr: true},
		{name: "int64 keeps width", existing: int64(1), input: 3.0, expected: int64(3)},
		{name: "float32 from string", existing: float32(1), input: "1.25", expected: float32(1.25)},
		{name: "float64 from int", existing: 1.0, input: 2, expected: 2.0},
		{name: "bool from word", existing: false, input: "on", expected: true},
		{name: "bool from number", existing: true, input: 0.0, expected: false},
		{name: "bool rejects text", existing: true, input: "maybe", wantErr: true},
		{name: "string from number", existing: "x", input: 3, expected: "3"},
		{name: "nil keeps the new value", existing: nil, input: "new", expected: "new"},
		{name: "unsupported type", existing: []any{}, input: 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Coerce(tt.existing, tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Coerce(%v, %v) = %v, want an error", tt.existing, tt.input, got)
				}

				return
			}

			if err != nil || got != tt.expected {
				t.Fatalf("Coerce(%v, %v) = %v (%T), %v; want %v (%T)", tt.existing, tt.input, got, got, err, tt.expected, tt.expected)
			}
		})
	}
}

func TestEncodeRejectsOversizedValues(t *testing.T) {
	t.Parallel()

	if _, err := Encode(int(1) << 40); err == nil {
		t.Fatal("expected an error for an int outside int32")
	}

	if _, err := Encode(string(make([]byte, 70000))); err == nil {
		t.Fatal("expected an error for a string longer than the uint16 length prefix")
	}
}
