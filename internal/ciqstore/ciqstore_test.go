package ciqstore

import (
	"os"
	"testing"
)

func TestParseEasyPlus(t *testing.T) {
	b, err := os.ReadFile("testdata/easyplus-v24-vivoactive5.html")
	if err != nil {
		t.Skip("no sample page")
	}
	s, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Settings) != 88 {
		t.Fatalf("expected 88 settings, got %d", len(s.Settings))
	}
	bar, ok := s.Get("bar1a")
	if !ok || bar.Type != "select" || bar.Label == "" || len(bar.Options) < 10 || bar.Default != "101" {
		t.Fatalf("bar1a: %+v", bar)
	}
	if bar.OptionLabel("107") != "Body Battery %" {
		t.Fatalf("bar1a option 107 = %q", bar.OptionLabel("107"))
	}
	if v, err := bar.OptionValue("body battery"); err != nil || v != "107" {
		t.Fatalf("OptionValue(body battery) = %q, %v", v, err)
	}
	sec, _ := s.Get("seconds")
	if sec.Type != "toggle" || sec.Default != "true" {
		t.Fatalf("seconds: %+v", sec)
	}
	c1, _ := s.Get("cust_color1")
	if c1.Type != "text" || c1.MaxLength != 6 || c1.Default != "FF0000" {
		t.Fatalf("cust_color1: %+v", c1)
	}
	ms, _ := s.Get("metrics_scale")
	if ms.Type != "number" || ms.Min != "0.6" || ms.Max != "1.7" {
		t.Fatalf("metrics_scale: %+v", ms)
	}
	ro, _ := s.Get("screen2_settings")
	if !ro.ReadOnly {
		t.Fatalf("screen2_settings should be read-only: %+v", ro)
	}
}
