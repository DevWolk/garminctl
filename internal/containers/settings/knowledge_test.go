package settings

import (
	"encoding/json"
	"sync"
	"testing"
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
