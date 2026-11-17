package apps

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func appHintsJSON(t *testing.T, value *AppHints) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// hintedAppID returns a store ID the embedded knowledge has hints for, so the
// tests follow that file instead of naming one app.
func hintedAppID(t *testing.T) string {
	t.Helper()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(ciqAppsKnowledge, &raw); err != nil {
		t.Fatal(err)
	}

	var ids []string

	for id := range raw {
		if !strings.HasPrefix(id, "_") {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		t.Fatal("embedded knowledge has no app hints")
	}

	slices.Sort(ids)

	return ids[0]
}

func TestAppHintsReturnsIndependentSnapshots(t *testing.T) {
	t.Parallel()

	appID := hintedAppID(t)

	first := AppHintsFor(appID)
	if first == nil || len(first.KeyHints) == 0 || len(first.ValueHints) == 0 {
		t.Fatal("fixture lacks expected app hints")
	}

	want := appHintsJSON(t, first)
	first.Name = "modified hint name"
	clear(first.KeyHints)
	clear(first.ValueHints)

	if got := appHintsJSON(t, AppHintsFor(strings.ToUpper(appID))); got != want {
		t.Fatal("app-hint snapshot changed global hints or case-insensitive lookup")
	}

	if AppHintsFor("absent-app-hints") != nil {
		t.Fatal("unknown app acquired hints")
	}
}

// The registry fixture holds only synthetic apps, so this registry adds one
// entry under a store ID with embedded hints.
func TestReadSettingsAttachesHintsByStoreID(t *testing.T) { //nolint:paralleltest // isolates process schema client and cache environment
	testdump.AppEnvironment(t)

	const file = "HINT0001"

	appID := hintedAppID(t)
	apps := append(registryFixtureApps(), registryFixtureApp{id: appID, kind: 3, name: "Hinted face", version: 1, file: file, size: 1})

	settings, err := ciq.Encode(ciq.Dict{{Key: "Enabled", Value: true}})
	if err != nil {
		t.Fatal(err)
	}

	dev, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	sess := appTestSession(t, dev)

	if err := dev.Mkdir(t.Context(), watchsession.PathAppSettings); err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string][]byte{
		PathAppsConfig: encodeRegistryFixture(t, apps),
		path.Join(watchsession.PathAppSettings, file+".SET"):            settings,
		path.Join(watchsession.PathAppSettings, testdump.AppRef+".SET"): settings,
	} {
		if err := dev.Write(t.Context(), name, data); err != nil {
			t.Fatal(err)
		}
	}

	read := NewReadSettingsAction(new(SchemaCache))

	hinted, err := read.Run(t.Context(), sess, file)
	if err != nil || hinted.App.ID != appID {
		t.Fatalf("hinted settings=%+v error=%v", hinted, err)
	}

	if got, want := appHintsJSON(t, hinted.Hints), appHintsJSON(t, AppHintsFor(appID)); hinted.Hints == nil || got != want {
		t.Fatalf("settings hints=%s, want %s", got, want)
	}

	encoded, err := json.Marshal(hinted)
	if err != nil || !strings.Contains(string(encoded), `"hints":{"name":`) {
		t.Fatalf("settings JSON lacks hints: %s, %v", encoded, err)
	}

	plain, err := read.Run(t.Context(), sess, testdump.AppRef)
	if err != nil || plain.Hints != nil {
		t.Fatalf("an app without embedded hints acquired some: %+v, %v", plain, err)
	}
}
