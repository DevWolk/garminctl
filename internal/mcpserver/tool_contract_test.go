package mcpserver

import (
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

func checkToolContracts(t *testing.T, want map[string]string) {
	t.Helper()
	client := newClient(t)

	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	seen := 0

	for _, tool := range tools.Tools {
		if hash, ok := want[tool.Name]; ok {
			seen++

			t.Run(tool.Name, func(t *testing.T) { t.Parallel(); testdump.CheckJSON(t, tool, hash) })
		}
	}

	if seen != len(want) {
		t.Fatalf("registered tools=%d want=%d", seen, len(want))
	}
}
