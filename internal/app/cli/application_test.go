package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/clix"
)

const (
	recoverySubcommand = "recovery"
	inspectSubcommand  = "inspect"
)

func TestApplicationRecoveryDispatchNeedsNoWatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{name: "help", args: []string{"-root", "/missing-watch", "application", "-h"}},
		{name: "invalid recovery args", args: []string{"-root", "/missing-watch", "application", recoverySubcommand, "export"}, code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out, diagnostics bytes.Buffer

			err := Run("v1.2.0", tc.args, &out, &diagnostics)
			if clix.ExitCode(err) != tc.code {
				t.Fatalf("code=%d error=%v", clix.ExitCode(err), err)
			}

			if bytes.Contains(diagnostics.Bytes(), []byte("unknown command")) || (err != nil && bytes.Contains([]byte(err.Error()), []byte("missing-watch"))) {
				t.Fatalf("recovery used watch dispatch: %v %s", err, &diagnostics)
			}
		})
	}
}

func TestApplicationRecoveryListNeedsNoCompiledKey(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)

	var out, diagnostics bytes.Buffer

	if err := Run("v1.2.0", []string{applicationCommandName, recoverySubcommand, "list"}, &out, &diagnostics); err != nil || !bytes.Contains(out.Bytes(), []byte("Retained metadata only")) {
		t.Fatalf("metadata listing: %v out=%q diagnostics=%q", err, &out, &diagnostics)
	}
}

func TestApplicationRecoveryInspectOfAnUnknownSnapshotSaysItIsNotRetained(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("APPDATA", directory)

	var out, diagnostics bytes.Buffer

	err := Run("v1.2.0", []string{applicationCommandName, recoverySubcommand, inspectSubcommand, strings.Repeat("ab", 32)}, &out, &diagnostics)
	if clix.ExitCode(err) != 1 || !strings.Contains(err.Error(), "not retained") || strings.Contains(err.Error(), directory) || out.Len() != 0 {
		t.Fatalf("inspect of a missing backup: error=%v out=%q", err, &out)
	}
}
