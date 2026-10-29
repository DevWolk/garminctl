package garmin

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

// Retain the pre-move JSON as an independent compatibility fixture.
//
//go:embed knowledge/device_settings.json
var deviceSettingsKnowledge []byte

func knowledgeJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestServiceKnowledgeAndSettingsViewsAreIndependent(t *testing.T) {
	t.Parallel()

	d, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	svc := New(d)
	want := knowledgeJSON(t, DefaultKnowledge())
	accessor := svc.Knowledge()
	clear(accessor.Fields)
	clear(accessor.Messages)
	accessor.ConnectManaged[0] = "accessor change"

	view, err := svc.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if knowledgeJSON(t, view.Knowledge) != want {
		t.Fatal("accessor mutation changed the settings view")
	}

	clear(view.Knowledge.Fields)
	clear(view.Knowledge.Messages)
	view.Knowledge.ConnectManaged[0] = "view change"

	next, err := svc.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if knowledgeJSON(t, next.Knowledge) != want || knowledgeJSON(t, svc.Knowledge()) != want {
		t.Fatal("a settings view modified published service knowledge")
	}
}

func TestAppHintsReturnsIndependentSnapshots(t *testing.T) {
	t.Parallel()

	const appID = "7c993e2b-70b3-459a-bbe0-db48a89af5a8"

	first := AppHintsFor(appID)
	if first == nil || len(first.KeyHints) == 0 || len(first.ValueHints) == 0 {
		t.Fatal("fixture lacks expected app hints")
	}

	want := knowledgeJSON(t, first)
	first.Name = "modified hint name"
	clear(first.KeyHints)
	clear(first.ValueHints)

	if got := knowledgeJSON(t, AppHintsFor(strings.ToUpper(appID))); got != want {
		t.Fatal("app-hint snapshot changed global hints or case-insensitive lookup")
	}

	if AppHintsFor("absent-app-hints") != nil {
		t.Fatal("unknown app acquired hints")
	}
}

func TestEmbeddedKnowledgeBytesUnchanged(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]struct {
		data []byte
		hash string
	}{
		"device_settings.json": {deviceSettingsKnowledge, "77d53ca2a2dcff5e823692e502577762f045074b62c987c1473b1793d3024bed"},
		"ciq_apps.json":        {ciqAppsKnowledge, "0579403f17b2b2865f4907c3d04e9b15ebd08253a98406471a70f5a060646ddd"},
	} {
		if got := fmt.Sprintf("%x", sha256.Sum256(fixture.data)); got != fixture.hash {
			t.Fatalf("%s changed during the knowledge ownership fix: %s", name, got)
		}
	}
}
