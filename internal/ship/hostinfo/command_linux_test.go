package hostinfo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLinuxPackageQueriesAreReadOnlyAndRecognizeUnownedFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "unowned-regular-file")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		kind probeCommand
	}{{"dpkg-query", probeDPKG}, {"rpm", probeRPM}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tool, err := exec.LookPath(tc.name)
			if err != nil {
				t.Skipf("native query unavailable: %v", err)
			}

			if _, err := runProbe(t.Context(), tc.kind, path); !errors.Is(err, errNoPackageOwner) {
				t.Fatalf("unowned query error=%v", err)
			}

			text, err := runProbe(t.Context(), tc.kind, tool)
			if errors.Is(err, errNoPackageOwner) {
				t.Log("query tool is installed outside this manager database")
				return
			}

			if err != nil {
				t.Fatalf("query own package: %v", err)
			}

			owner, err := packageOwner(tc.kind, tool, text)
			if err != nil || owner.Package == "" {
				t.Fatalf("owner=%+v error=%v", owner, err)
			}
		})
	}
}
