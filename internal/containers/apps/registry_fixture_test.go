package apps

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

// registryFixturePath is the OUT.BIN that testdump copies into every dump.
var registryFixturePath = filepath.Join("..", "..", "testdump", "testdata", "OUT.BIN")

// registryFixtureApp is one synthetic registry entry. Built-in entries have a
// native ID; Connect IQ entries have a version, file name and size.
type registryFixtureApp struct {
	id       string
	kind     uint64
	name     string
	hidden   bool
	version  uint64
	file     string
	size     uint64
	nativeID uint64
}

const (
	registryFixtureStore   = "5a000000-0000-4000-8000-0000000000a1"
	registryFixtureTracker = "5a000000-0000-4000-8000-0000000000b2"
	registryFixtureRoutes  = "5a000000-0000-4000-8000-0000000000c3"
	registryFixtureAudio   = "5a000000-0000-4000-8000-0000000000e5"
)

// registryFixtureApps keeps every entry kind the tests rely on: built-in and
// Connect IQ apps, glances and activities, one watch face, one audio provider,
// shown and hidden entries, and names repeated across and within types.
func registryFixtureApps() []registryFixtureApp {
	return []registryFixtureApp{
		{id: "c0ffee15-600d-2088-0000-000000000000", kind: 1, name: "Timer", nativeID: 136},
		{id: "c0ffee15-600d-2009-0000-000000000000", kind: 1, name: "Music", nativeID: 9},
		{id: "c0ffee15-600d-2089-0000-000000000000", kind: 1, name: "Stopwatch", nativeID: 137},
		{id: registryFixtureStore, kind: 1, name: "Connect IQ Store", hidden: true, version: 68, file: "Toystore", size: 65536},
		{id: "c0ffee15-600d-20f4-0000-000000000000", kind: 1, name: "Tips", hidden: true, nativeID: 244},
		{id: "c0ffee15-600d-200a-0000-000000000000", kind: 1, name: "Weather", nativeID: 10},
		{id: "c0ffee15-600d-202c-0000-000000000000", kind: 1, name: "Steps", hidden: true, nativeID: 44},
		{id: registryFixtureTracker, kind: 1, name: "Fixture Tracker", hidden: true, version: 40, file: "Tracker", size: 16384},
		{id: "c0ffee15-600d-2007-0000-000000000000", kind: 1, name: "Calendar", nativeID: 7},
		{id: "c0ffee15-600d-200a-0000-000000000000", kind: 2, name: "Weather", nativeID: 10},
		{id: "c0ffee15-600d-202c-0000-000000000000", kind: 2, name: "Steps", nativeID: 44},
		{id: registryFixtureTracker, kind: 2, name: "Fixture Tracker", hidden: true, version: 40, file: "Tracker", size: 16384},
		{id: "c0ffee15-600d-2007-0000-000000000000", kind: 2, name: "Calendar", nativeID: 7},
		{id: "c0ffee15-600d-204c-0000-000000000000", kind: 2, name: "Stress", nativeID: 76},
		{id: registryFixtureRoutes, kind: 2, name: "Fixture Routes", version: 221, file: "ROUTE001", size: 32768},
		{id: testdump.StoreAppID, kind: 3, name: testdump.AppName, hidden: true, version: 23, file: testdump.AppRef, size: 4096},
		{id: registryFixtureAudio, kind: 7, name: "Fixture Audio", hidden: true, version: 19, file: "AUDIO001", size: 8192},
		{id: "c0ffee15-600d-5403-0000-000000000000", kind: 8, name: "Walk", nativeID: 16},
		{id: "c0ffee15-600d-5400-0000-000000000000", kind: 8, name: "Run", hidden: true, nativeID: 13},
		{id: "c0ffee15-600d-5401-0000-000000000000", kind: 8, name: "Treadmill", hidden: true, nativeID: 23},
		{id: "c0ffee15-600d-5402-0000-000000000000", kind: 8, name: "Treadmill", hidden: true, nativeID: 75},
		{id: "c0ffee15-600d-540f-0000-000000000000", kind: 8, name: "Yoga", nativeID: 79},
		{id: registryFixtureRoutes, kind: 8, name: "Fixture Routes", version: 221, file: "ROUTE001", size: 32768},
		{id: "c0ffee15-600d-5405-0000-000000000000", kind: 8, name: "Bike", nativeID: 14},
	}
}

// decoded is the App the registry parser reports for the entry.
func (a registryFixtureApp) decoded() App {
	//nolint:gosec // fixture values are the small literals of registryFixtureApps
	return App{
		ID: a.id, Type: int(a.kind), TypeName: appTypeNames[int(a.kind)], Name: a.name, Hidden: a.hidden,
		Version: int(a.version), FileName: a.file, Size: int(a.size), NativeID: int(a.nativeID), Native: a.file == "",
	}
}

// fields lays an entry out as the watch does: field 4 is always present and
// the undocumented field 9 closes the record.
func (a registryFixtureApp) fields(t *testing.T) []pbField {
	t.Helper()

	id, err := hex.DecodeString(strings.ReplaceAll(a.id, "-", ""))
	if err != nil || len(id) != 16 {
		t.Fatalf("registry fixture id %q: %v", a.id, err)
	}

	hidden := uint64(0)
	if a.hidden {
		hidden = 1
	}

	fields := []pbField{
		{num: 1, wire: 2, data: id},
		{num: 2, wire: 0, varint: a.kind},
		{num: 3, wire: 2, data: []byte(a.name)},
		{num: 4, wire: 0, varint: hidden},
	}

	if a.file == "" {
		fields = append(fields, pbField{num: 8, wire: 0, varint: a.nativeID})
	} else {
		fields = append(fields,
			pbField{num: 5, wire: 0, varint: a.version},
			pbField{num: 6, wire: 2, data: []byte(a.file)},
			pbField{num: 7, wire: 0, varint: a.size})
	}

	return append(fields, pbField{num: 9, wire: 0})
}

// encodeRegistryFixture nests the entries as root.3 -> config.2 -> list.3,
// after the two leading list fields the code does not interpret.
func encodeRegistryFixture(t *testing.T, apps []registryFixtureApp) []byte {
	t.Helper()

	list := []pbField{{num: 1, wire: 0, varint: 1}, {num: 2, wire: 0, varint: 27}}
	for _, app := range apps {
		list = append(list, pbField{num: 3, wire: 2, data: pbEncode(app.fields(t))})
	}

	config := pbEncode([]pbField{{num: 2, wire: 2, data: pbEncode(list)}})

	return pbEncode([]pbField{{num: 3, wire: 2, data: config}})
}

func TestRegistryFixtureMatchesGenerator(t *testing.T) {
	t.Parallel()
	testdump.CheckFixture(t, registryFixturePath, encodeRegistryFixture(t, registryFixtureApps()))
}

func TestRegistryFixtureDecodesToItsDefinition(t *testing.T) {
	t.Parallel()

	want := registryFixtureApps()

	got, err := ParseAppsConfig(encodeRegistryFixture(t, want))
	if err != nil || len(got) != len(want) {
		t.Fatalf("decoded %d of %d entries: %v", len(got), len(want), err)
	}

	for i, app := range got {
		if expected := want[i].decoded(); app != expected {
			t.Errorf("entry %d = %+v, want %+v", i, app, expected)
		}
	}
}
