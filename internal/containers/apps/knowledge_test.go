package apps

import (
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func TestEmbeddedAppKnowledgeBytesUnchanged(t *testing.T) {
	t.Parallel()
	testdump.CheckDigest(t, ciqAppsKnowledge, "0579403f17b2b2865f4907c3d04e9b15ebd08253a98406471a70f5a060646ddd")
}
