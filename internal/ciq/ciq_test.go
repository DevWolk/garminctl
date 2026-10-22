package ciq

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestEasyPlusValues(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/G5RG4154.SET")
	if err != nil {
		t.Fatal(err)
	}

	v, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	d, ok := v.(Dict)
	if !ok || len(d) != 88 {
		t.Fatalf("expected 88-entry dict, got %T len %d", v, len(d))
	}

	if c, _ := d.Get("cust_color1"); c != "FF7A2A" {
		t.Fatalf("cust_color1 = %v", c)
	}

	if s, _ := d.Get("seconds"); s != true {
		t.Fatalf("seconds = %v", s)
	}

	if s, _ := d.Get("metrics_scale"); s != float32(1.0) {
		t.Fatalf("metrics_scale = %v", s)
	}

	if s, _ := d.Get("field5a"); s != int32(14) {
		t.Fatalf("field5a = %v", s)
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
