package apps

import (
	"os"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func TestParseAppsConfig(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(registryFixturePath)
	if err != nil {
		t.Fatal(err)
	}

	apps, err := ParseAppsConfig(b)
	if err != nil {
		t.Fatal(err)
	}

	var face *App

	for i := range apps {
		if apps[i].FileName == testdump.AppRef {
			face = &apps[i]
		}
	}

	if face == nil {
		t.Fatalf("%s not found among %d apps", testdump.AppRef, len(apps))
	}

	if face.Name != testdump.AppName || face.TypeName != "watchface" || face.ID != testdump.StoreAppID {
		t.Fatalf("unexpected watch face entry: %+v", *face)
	}
}

func TestRewriteAppsRoundTrip(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(registryFixturePath)
	if err != nil {
		t.Fatal(err)
	}

	same, err := rewriteApps(b, func(_ App, f []pbField) ([]pbField, error) { return f, nil })
	if err != nil {
		t.Fatal(err)
	}

	if string(same) != string(b) {
		t.Fatalf("identity rewrite changed %d bytes", len(b))
	}

	flipped, err := rewriteApps(b, func(a App, f []pbField) ([]pbField, error) {
		if a.Name != testdump.AppName {
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
		if a.Name == testdump.AppName && a.Hidden {
			t.Fatal("flag not flipped")
		}
	}
}
