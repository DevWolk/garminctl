package garmin

import (
	"os"
	"testing"
)

func TestParseDeviceXML(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../testdump/testdata/GarminDevice.xml")
	if err != nil {
		t.Fatal(err)
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
	t.Parallel()

	b, err := os.ReadFile("../testdump/testdata/GarminDevice.xml")
	if err != nil {
		t.Fatal(err)
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
