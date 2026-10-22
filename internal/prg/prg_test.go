package prg

import (
	"os"
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	b, err := os.ReadFile("testdata/easyplus.prg")
	if err != nil {
		t.Skip("no sample PRG")
	}
	d, err := DefaultSettings(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 88 {
		t.Fatalf("expected 88 defaults, got %d", len(d))
	}
	if v, _ := d.Get("cust_color1"); v != "FF0000" {
		t.Fatalf("cust_color1 default = %v", v)
	}
	strs, err := StringResources(b)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range strs {
		if s == "Layout type" {
			found = true
		}
	}
	if !found {
		t.Fatalf("label 'Layout type' not found among %d strings", len(strs))
	}
}
