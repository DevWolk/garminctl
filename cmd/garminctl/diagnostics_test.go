package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestWatchDiagnosticDispatchRetainsUnsupportedErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		args    []string
		code    int
		message string
	}{
		{name: "hidden", args: []string{"hidden", "GARMIN"}, code: 1, message: "hidden requires the Windows MTP bridge"},
		{name: "raw capability before hex", args: []string{"mtp", "not-hex"}, code: 2, message: "usage: mtp OPCODE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out, diagnostics bytes.Buffer

			args := append([]string{rootFlag, testdump.Root(t)}, test.args...)

			err := run(args, &out, &diagnostics)
			if exitCode(err) != test.code || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("diagnostic error=%v exit=%d", err, exitCode(err))
			}
		})
	}
}
