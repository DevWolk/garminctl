package settings

import (
	"testing"

	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestReadActionKnowledgeViewsAreIndependent(t *testing.T) {
	t.Parallel()

	sess, _ := actionFixture(t)
	read := NewReadAction(fit.MustProfile())

	want := knowledgeJSON(t, DefaultKnowledge())
	accessor := DefaultKnowledge()
	clear(accessor.Fields)
	clear(accessor.Messages)
	accessor.ConnectManaged[0] = "accessor change"

	view, err := read.Run(t.Context(), sess)
	if err != nil {
		t.Fatal(err)
	}

	if knowledgeJSON(t, view.Knowledge) != want {
		t.Fatal("accessor mutation changed the settings view")
	}

	clear(view.Knowledge.Fields)
	clear(view.Knowledge.Messages)
	view.Knowledge.ConnectManaged[0] = "view change"

	next, err := read.Run(t.Context(), sess)
	if err != nil {
		t.Fatal(err)
	}

	if knowledgeJSON(t, next.Knowledge) != want || knowledgeJSON(t, DefaultKnowledge()) != want {
		t.Fatal("a settings view modified published knowledge")
	}
}

func TestEmbeddedKnowledgeBytesUnchanged(t *testing.T) {
	t.Parallel()

	testdump.CheckDigest(t, deviceSettingsKnowledge, "77d53ca2a2dcff5e823692e502577762f045074b62c987c1473b1793d3024bed")
}
