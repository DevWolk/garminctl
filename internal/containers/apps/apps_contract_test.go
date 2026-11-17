package apps

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const appModeKey = "Mode"

func TestAppDTOZeroContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		in   any
		want string
	}{
		{name: "app", in: App{}, want: `{"id":"","type":0,"typeName":"","name":"","hidden":false,"native":false}`},
		{name: "settings", in: AppSettings{}, want: `{"app":{"id":"","type":0,"typeName":"","name":"","hidden":false,"native":false},"path":"","values":null,"order":null,"items":null}`},
		{name: "change", in: AppChange{}, want: `{"id":"","name":"","typeName":"","hidden":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(test.in)
			if err != nil || string(got) != test.want {
				t.Fatalf("JSON=%s error=%v want=%s", got, err, test.want)
			}
		})
	}
}

func TestAppSettingsEditContracts(t *testing.T) { //nolint:paralleltest // isolates process schema client and cache environment
	testdump.AppEnvironment(t)
	dev, _ := testdump.Apps(t)
	sess := appTestSession(t, dev)
	apply := NewApplySettingsAction(new(SchemaCache))

	const path = "GARMIN/Apps/SETTINGS/" + testdump.AppRef + ".SET"

	before, err := dev.Read(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct { //nolint:paralleltest // invalid edits share one Session and compare the same original bytes
		name string
		key  string
		in   any
	}{
		{name: "read only", key: "Locked", in: 2},
		{name: "missing key", key: "Missing", in: 2},
		{name: "invalid option", key: appModeKey, in: "missing"},
		{name: "text limit", key: "Title", in: "longer than twelve"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := apply.Run(t.Context(), sess, testdump.AppRef, map[string]any{test.key: test.in}); err == nil {
				t.Fatal("invalid edit succeeded")
			}

			after, err := dev.Read(t.Context(), path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed edit changed bytes: %v", err)
			}
		})
	}

	updated, err := apply.Run(t.Context(), sess, "face", map[string]any{appModeKey: "Night"})
	if err != nil || updated.Values[appModeKey] != int32(2) {
		t.Fatalf("updated=%+v error=%v", updated, err)
	}

	after, err := dev.Read(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	value, err := ciq.Decode(after)
	if err != nil {
		t.Fatal(err)
	}

	expected := ciq.Dict{
		{Key: appModeKey, Value: int32(2)},
		{Key: "Enabled", Value: true},
		{Key: "Title", Value: "Fixture"},
		{Key: "Locked", Value: int32(7)},
		{Key: "Unknown", Value: []any{int64(1 << 40), nil, float32(1.5)}},
	}
	if !reflect.DeepEqual(value, expected) {
		t.Fatalf("stored order/types/unknown values changed: %#v", value)
	}
}

func TestAppSettingsOfflineFallbackContract(t *testing.T) { //nolint:paralleltest // isolates process schema client and cache environment
	testdump.AppEnvironment(t)

	if err := os.RemoveAll(ciqstore.CacheDir()); err != nil {
		t.Fatal(err)
	}

	dev, _ := testdump.Apps(t)
	sess := appTestSession(t, dev)
	cache := new(SchemaCache)

	view, err := NewReadSettingsAction(cache).Run(t.Context(), sess, testdump.AppRef)
	if err != nil || view.SchemaSource != "" || !strings.Contains(view.SchemaError, "offline app contract fixture") {
		t.Fatalf("fallback=%+v error=%v", view, err)
	}

	if view.Items[0].Type != "int" || view.Items[0].ValueLabel != "" || view.Items[0].Options != 0 {
		t.Fatalf("fallback decoration=%+v", view.Items[0])
	}

	updated, err := NewApplySettingsAction(cache).Run(t.Context(), sess, testdump.AppRef, map[string]any{appModeKey: "2"})
	if err != nil || updated.Values[appModeKey] != int32(2) {
		t.Fatalf("fallback edit=%+v error=%v", updated, err)
	}
}

func TestAppBinaryInstallAndRemoveContracts(t *testing.T) {
	t.Parallel()

	dev, _ := testdump.Apps(t)
	sess := appTestSession(t, dev)
	cache := new(SchemaCache)

	registry, err := dev.Read(t.Context(), PathAppsConfig)
	if err != nil {
		t.Fatal(err)
	}

	data := []byte{0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}

	target, err := NewInstallPRGAction(cache).Run(t.Context(), sess, data)
	if err != nil || !regexp.MustCompile(`^GARMIN/APPS/MEDIA/[0-9A-F]{8}\.PRG$`).MatchString(target) {
		t.Fatalf("install path=%q error=%v", target, err)
	}

	back, err := dev.Read(t.Context(), target)
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("PRG bytes changed: %v", err)
	}

	removed, err := NewRemoveAction(cache).Run(t.Context(), sess, testdump.AppRef+".PRG")

	want := []string{
		"GARMIN/APPS/MEDIA/" + testdump.AppRef + ".PRG", "GARMIN/Apps/SETTINGS/" + testdump.AppRef + ".SET",
		"GARMIN/Apps/DATA/" + testdump.AppRef + ".DAT", "GARMIN/Apps/DATA/" + testdump.AppRef + ".IDX", "GARMIN/Apps/DATA/" + testdump.AppRef + ".IMT",
	}
	if err != nil || !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed=%v error=%v", removed, err)
	}

	for _, path := range want {
		if _, err := dev.Stat(t.Context(), path); err == nil {
			t.Fatalf("removed file remains: %s", path)
		}
	}

	after, err := dev.Read(t.Context(), PathAppsConfig)
	if err != nil || !bytes.Equal(after, registry) {
		t.Fatalf("binary install/removal changed registry bytes: %v", err)
	}
}

func TestRemoveRefusesProtectedRegistryEntries(t *testing.T) {
	t.Parallel()

	dev, _ := testdump.Apps(t)
	sess := appTestSession(t, dev)

	registry, err := dev.Read(t.Context(), PathAppsConfig)
	if err != nil {
		t.Fatal(err)
	}

	for ref, want := range map[string]string{
		"Toystore": "the Connect IQ Store app cannot be removed",
		"Timer":    `"Timer" is not an installed Connect IQ app`,
	} {
		removed, err := NewRemoveAction(new(SchemaCache)).Run(t.Context(), sess, ref)
		if err == nil || err.Error() != want || len(removed) != 0 {
			t.Fatalf("remove %s: removed=%v error=%v", ref, removed, err)
		}
	}

	after, err := dev.Read(t.Context(), PathAppsConfig)
	if err != nil || !bytes.Equal(after, registry) {
		t.Fatalf("refused removal changed registry bytes: %v", err)
	}
}
