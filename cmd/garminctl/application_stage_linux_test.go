//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestInstallerLostReplyDoesNotExitOnSIGPIPE(t *testing.T) {
	t.Parallel()

	const marker = "GARMINCTL_TEST_INSTALLER_PIPE"

	if os.Getenv(marker) == "1" {
		err := installerPipeScope(func() error {
			_, err := os.Stdout.Write([]byte("accepted\n"))
			return err
		})
		if !errors.Is(err, syscall.EPIPE) {
			os.Exit(3)
		}

		os.Exit(0)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestInstallerLostReplyDoesNotExitOnSIGPIPE$") //nolint:gosec // Only this test executable and fixed test selector; no installer or user-selected command.

	child.Env = append(os.Environ(), marker+"=1")
	child.Stdout = writer
	child.Stderr = os.Stderr

	if err := child.Run(); err != nil {
		t.Fatalf("lost reply killed installer helper: %v", err)
	}
}
