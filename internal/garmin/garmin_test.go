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
