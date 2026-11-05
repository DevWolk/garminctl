package contracts

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

func TestCloudStatusSchemaAndAnnotations(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	tools, err := newMCPContractClient(t).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range tools.Tools {
		if tool.Name != "cloud_status" {
			continue
		}

		data, err := json.Marshal(map[string]any{"output": tool.OutputSchema, "annotations": tool.Annotations})
		if err != nil {
			t.Fatal(err)
		}

		const want = "586dfd614807725892a6841251c1edb2c48416ebfe45254738d4589b7e112c01"
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
			t.Fatalf("cloud_status contract changed: SHA256=%s JSON=%s", got, data)
		}

		return
	}

	t.Fatal("cloud_status tool is missing")
}
