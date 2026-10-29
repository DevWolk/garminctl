package apps

import (
	"os"
	"testing"
)

func TestParseAppsConfig(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../garmin/testdata/OUT.BIN")
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

func TestRewriteAppsRoundTrip(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../garmin/testdata/OUT.BIN")
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
		if a.Name == "Easy+" && a.Hidden {
			t.Fatal("flag not flipped")
		}
	}
}
