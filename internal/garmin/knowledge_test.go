package garmin

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestKnowledgeConcurrentReadHasNoLazyMutation(t *testing.T) {
	t.Parallel()

	k := &Knowledge{ConnectManaged: []string{"2.1", "2.2"}}
	start := make(chan struct{})

	var readers sync.WaitGroup
	for range 16 {
		readers.Go(func() {
			<-start

			for range 32 {
				if !k.ManagedByConnect(2, 1) || k.ManagedByConnect(2, 3) {
					t.Error("connect-managed membership changed during read")
				}
			}
		})
	}

	close(start)
	readers.Wait()

	if k.managed != nil {
		t.Fatal("a read initialized shared mutable state")
	}
}

func knowledgeJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestDefaultKnowledgeReturnsIndependentSnapshots(t *testing.T) {
	t.Parallel()

	var original Knowledge
	if err := json.Unmarshal(deviceSettingsKnowledge, &original); err != nil {
		t.Fatal(err)
	}

	want := knowledgeJSON(t, &original)

	first := DefaultKnowledge()
	if got := knowledgeJSON(t, first); got != want {
		t.Fatal("snapshot changed the knowledge JSON contract")
	}

	clear(first.Messages)
	clear(first.Fields)

	if len(first.ConnectManaged) == 0 {
		t.Fatal("fixture lacks the Connect-managed membership list")
	}

	first.ConnectManaged[0] = "modified snapshot"

	if got := knowledgeJSON(t, DefaultKnowledge()); got != want {
		t.Fatal("default knowledge can be mutated through its public snapshot")
	}

	if defaultKnowledge().managed == nil {
		t.Fatal("canonical knowledge was published without its derived index")
	}
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
