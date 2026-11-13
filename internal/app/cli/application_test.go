package cli

import (
	"bytes"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/clix"
)

func TestApplicationRecoveryDispatchNeedsNoWatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{name: "help", args: []string{"-root", "/missing-watch", "application", "-h"}},
		{name: "metadata without compiled key", args: []string{"-root", "/missing-watch", "application", "recovery", "list"}, code: 1},
		{name: "invalid recovery args", args: []string{"-root", "/missing-watch", "application", "recovery", "export"}, code: 2},
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
