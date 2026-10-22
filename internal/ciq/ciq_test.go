package ciq

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.SET")
	if len(files) == 0 {
		t.Fatal("no testdata")
	}
	for _, path := range files {
		data, _ := os.ReadFile(path)
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
	data, _ := os.ReadFile("testdata/G5RG4154.SET")
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
