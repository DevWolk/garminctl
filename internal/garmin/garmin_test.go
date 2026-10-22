package garmin

import (
	"os"
	"testing"
)

func TestParseAppsConfig(t *testing.T) {
	b, err := os.ReadFile("testdata/OUT.BIN")
	if err != nil {
		t.Skip("no sample")
	}
	apps, err := ParseAppsConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	var easy *App
	for i := range apps {
		if apps[i].FileName == "G5RG4154" {
			easy = &apps[i]
		}
	}
	if easy == nil {
		t.Fatalf("Easy+ not found among %d apps", len(apps))
	}
	if easy.Name != "Easy+" || easy.TypeName != "watchface" || easy.ID != "7c993e2b-70b3-459a-bbe0-db48a89af5a8" {
		t.Fatalf("unexpected Easy+ entry: %+v", *easy)
	}
}

func TestParseDeviceXML(t *testing.T) {
	b, err := os.ReadFile("testdata/GarminDevice.xml")
	if err != nil {
		t.Skip("no sample")
	}
	d, err := ParseDeviceXML(b)
	if err != nil {
		t.Fatal(err)
	}
	if d.UnitID != 1234567890 || d.Description != "vívoactive 5" || d.SoftwareVersionString() != "17.05" {
		t.Fatalf("unexpected: %+v", *d)
	}
	if len(d.DataTypes) < 30 {
		t.Fatalf("expected many data types, got %d", len(d.DataTypes))
	}
}

func TestParseDeviceXMLApps(t *testing.T) {
	b, err := os.ReadFile("testdata/GarminDevice.xml")
	if err != nil {
		t.Skip("no fixture")
	}
	d, err := ParseDeviceXML(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Apps) == 0 {
		t.Fatal("no apps parsed from Extensions/IQAppExt/Apps")
	}
	var provider *XMLApp
	for i := range d.Apps {
		if d.Apps[i].Type == AudioProviderType {
			provider = &d.Apps[i]
		}
	}
	if provider == nil {
		t.Fatalf("no audio provider among %d apps", len(d.Apps))
	}
	if provider.Name == "" || provider.MediaCacheBytes <= 0 {
		t.Fatalf("provider %+v lacks name or cache size", *provider)
	}
}

func TestRewriteAppsRoundTrip(t *testing.T) {
	b, err := os.ReadFile("testdata/OUT.BIN")
	if err != nil {
		t.Skip("no sample")
	}
	same, err := rewriteApps(b, func(_ App, f []pbField) ([]pbField, error) { return f, nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(same) != string(b) {
		t.Fatalf("identity rewrite changed %d bytes", len(b))
	}
	flipped, err := rewriteApps(b, func(a App, f []pbField) ([]pbField, error) {
		if a.Name != "Easy+" {
			return f, nil
		}
		for i := range f {
			if f[i].num == 4 {
				f[i].varint = 0
			}
		}
		return f, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(flipped) != len(b) {
		t.Fatalf("flag flip changed the length: %d -> %d", len(b), len(flipped))
	}
	apps, err := ParseAppsConfig(flipped)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if a.Name == "Easy+" && a.Enabled {
			t.Fatal("flag not flipped")
		}
	}
}
