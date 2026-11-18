package contracts

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func checkToolContracts(t *testing.T, want map[string]string) {
	t.Helper()
	checkClientToolContracts(t, newMCPContractClient(t), want)
}

func checkClientToolContracts(t *testing.T, client *mcp.ClientSession, want map[string]string) {
	t.Helper()

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

func checkMCPResultContract(t *testing.T, client *mcp.ClientSession, tool string, args any, hash string) {
	t.Helper()

	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}

	testdump.CheckJSON(t, result, hash)
}
