//go:build !windows

package updatetransport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStageProcessRetriesOnlyUnstartedBusyExecutable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "private-child")

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // only this test's private temporary executable fixture.
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	if _, err := file.WriteString("#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}

	if err := file.Chmod(0o700); err != nil {
		t.Fatal(err)
	}

	starts := 0

	err = runStageProcess(t.Context(), func() *exec.Cmd {
		starts++
		if starts == 2 {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}

		return exec.CommandContext(t.Context(), path)
	})
	if err != nil || starts < 2 || starts > 5 {
		t.Fatalf("launch error=%v attempts=%d", err, starts)
	}
}

func TestStageProcessDoesNotRetryStartedOrOtherFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "started child", args: []string{"/bin/sh", "-c", "exit 7"}},
		{name: "not found", args: []string{filepath.Join(t.TempDir(), "missing")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			starts := 0

			err := runStageProcess(t.Context(), func() *exec.Cmd { starts++; return exec.CommandContext(t.Context(), tc.args[0], tc.args[1:]...) }) //nolint:gosec // fixed local fixture programs, never request values.
			if err == nil || starts != 1 || errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v attempts=%d", err, starts)
			}
		})
	}
}
