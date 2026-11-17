package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/paths"
)

func TestApplicationStatusPrintsTheInstallerRecordWithoutWatchOrKey(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)

	var out, diagnostics bytes.Buffer
	if err := Run("v1.2.0", []string{"-root", "/missing-watch", applicationCommandName, "status"}, &out, &diagnostics); err != nil {
		t.Fatalf("status without a record: %v %s", err, &diagnostics)
	}

	if !strings.Contains(out.String(), "No installation") {
		t.Fatalf("status without a record=%q", out.String())
	}

	operation := filepath.Join(paths.Cache(), "application-installing", "stage-fixture")
	if err := os.MkdirAll(operation, 0o700); err != nil {
		t.Fatal(err)
	}

	record := `{"operation":"fixture","tag":"v1.3.0","status":"failed","error":"exit status 126"}`
	if err := os.WriteFile(filepath.Join(operation, "install-result.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()

	if err := Run("v1.2.0", []string{applicationCommandName, "status"}, &out, &diagnostics); err != nil {
		t.Fatalf("status with a record: %v %s", err, &diagnostics)
	}

	for _, part := range []string{"v1.3.0", "did not complete: exit status 126", operation} {
		if !strings.Contains(out.String(), part) {
			t.Fatalf("status %q lacks %q", out.String(), part)
		}
	}
}
