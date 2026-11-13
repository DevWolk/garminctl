//go:build linux

package updatetransport

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestInstallerOwnerWaitsOnActualProcessAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	child := exec.CommandContext(t.Context(), "/bin/sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })

	owner, err := openInstallerOwner(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := owner.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("live owner wait=%v", err)
	}

	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}

	bound, finish := context.WithTimeout(t.Context(), 3*time.Second)
	defer finish()

	if err := owner.Wait(bound); err != nil {
		t.Fatalf("exited owner wait=%v", err)
	}
}

func TestInstallerMonitoringCancellationLeavesCommittedProcessRunning(t *testing.T) {
	t.Parallel()

	child := exec.CommandContext(t.Context(), "/bin/sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = child.Process.Kill() })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := waitNativeInstaller(ctx, child); !errors.Is(err, errInstallerOutcomeUnknown) {
		t.Fatalf("committed monitoring=%v", err)
	}

	owner, err := openInstallerOwner(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()

	bound, finish := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer finish()

	if err := owner.Wait(bound); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("committed process was killed by monitoring cancellation: %v", err)
	}
}
