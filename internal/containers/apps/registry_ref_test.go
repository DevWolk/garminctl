package apps

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const (
	fixtureWeatherID   = "c0ffee15-600d-200a-0000-000000000000"
	fixtureTrackerName = "Fixture Tracker"
	fixtureTrackerFile = "Tracker"
)

func TestVisibilityAddressesOneEntryByKind(t *testing.T) { //nolint:paralleltest // registryCache changes process cache-directory environment
	registryCache(t)

	d, _, _ := registryFixture(t)
	sess := watchsession.New(d)

	for _, ref := range []string{"Weather", fixtureWeatherID, fixtureTrackerName, fixtureTrackerFile, registryFixtureTracker} {
		_, err := (VisibilityAction{}).Run(t.Context(), sess, ref, true)
		if err == nil || !strings.Contains(err.Error(), "name one as kind:id") || d.writes != 0 {
			t.Fatalf("ambiguous %q: %v, writes=%d", ref, err, d.writes)
		}
	}

	_, err := (VisibilityAction{}).Run(t.Context(), sess, "Weather", true)
	if err == nil || !strings.Contains(err.Error(), "app:"+fixtureWeatherID) || !strings.Contains(err.Error(), "glance:"+fixtureWeatherID) {
		t.Fatalf("ambiguity error does not list the entry refs: %v", err)
	}

	for _, ref := range []string{"glance:" + fixtureWeatherID, "Activity:Run", "glance:" + fixtureTrackerFile} {
		if _, err := (VisibilityAction{}).Run(t.Context(), sess, ref, true); err != nil {
			t.Fatalf("%q: %v", ref, err)
		}
	}

	in, err := d.Read(t.Context(), PathAppsIn)
	if err != nil {
		t.Fatal(err)
	}

	queued, err := ParseAppsConfig(in)
	if err != nil {
		t.Fatal(err)
	}

	hidden := map[string]bool{}
	for _, a := range queued {
		hidden[entryRef(a)] = a.Hidden
	}

	if !hidden["glance:"+fixtureWeatherID] || hidden["app:"+fixtureWeatherID] {
		t.Fatalf("weather glance/app hidden = %v/%v, want true/false", hidden["glance:"+fixtureWeatherID], hidden["app:"+fixtureWeatherID])
	}
}

func TestInstalledAppRefusesRefsOfSeveralApps(t *testing.T) {
	t.Parallel()

	registry := []App{
		{ID: "a", TypeName: "app", Name: "Clock", FileName: "CLOCK001"},
		{ID: "a", TypeName: "glance", Name: "Clock", FileName: "CLOCK001"},
		{ID: "b", TypeName: "watchface", Name: "Clock", FileName: "CLOCK002"},
	}

	if app, err := installedApp(registry, "Clock"); err == nil || !strings.Contains(err.Error(), "CLOCK001, CLOCK002") {
		t.Fatalf("ambiguous name accepted: %+v, %v", app, err)
	}

	if app, err := installedApp(registry, "a"); err != nil || app.FileName != "CLOCK001" {
		t.Fatalf("entries of one app refused: %+v, %v", app, err)
	}

	if app, err := installedApp(registry, "CLOCK002"); err != nil || app.ID != "b" {
		t.Fatalf("file id: %+v, %v", app, err)
	}
}

func TestFindAppRefusesAmbiguousRefs(t *testing.T) {
	t.Parallel()

	dev, _ := testdump.Apps(t)
	sess := appTestSession(t, dev)

	if app, err := (FindAction{}).Run(t.Context(), sess, "fixture"); err == nil {
		t.Fatalf("partial name of several apps accepted: %+v", app)
	}

	for ref, file := range map[string]string{fixtureTrackerName: fixtureTrackerFile, "track": fixtureTrackerFile, "routes": "ROUTE001"} {
		if app, err := (FindAction{}).Run(t.Context(), sess, ref); err != nil || app.FileName != file {
			t.Fatalf("%q: %+v, %v", ref, app, err)
		}
	}
}

// statFailDevice fails Stat for one path with an error other than absence.
type statFailDevice struct {
	device.Device
	path string
}

func (d *statFailDevice) Stat(ctx context.Context, path string) (device.Entry, error) {
	if strings.EqualFold(path, d.path) {
		return device.Entry{}, errSyntheticStat
	}

	return d.Device.Stat(ctx, path)
}

var errSyntheticStat = errors.New("synthetic stat failure")

func TestRemoveFailsWhenSidecarStatFails(t *testing.T) {
	t.Parallel()

	base, _ := testdump.Apps(t)
	dat := "GARMIN/Apps/DATA/" + testdump.AppRef + ".DAT"
	sess := appTestSession(t, &statFailDevice{Device: base, path: dat})

	removed, err := NewRemoveAction(new(SchemaCache)).Run(t.Context(), sess, testdump.AppRef)
	if !errors.Is(err, errSyntheticStat) {
		t.Fatalf("stat failure ignored: removed=%v, %v", removed, err)
	}

	if _, err := base.Stat(t.Context(), dat); err != nil {
		t.Fatalf("data file gone after a failed check: %v", err)
	}
}
