package prg

import (
	"os"
	"slices"
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("testdata/easyplus.prg")
	if err != nil {
		t.Fatal(err)
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

	if !slices.Contains(strs, "Layout type") {
		t.Fatalf("label 'Layout type' not found among %d strings", len(strs))
	}
}
