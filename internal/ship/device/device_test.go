package device

import (
	"context"
	"testing"
	"time"
)

type ctxKey struct{}

func TestDetachedOutlivesCallerButStaysBounded(t *testing.T) {
	t.Parallel()

	caller, cancel := context.WithCancel(context.WithValue(t.Context(), ctxKey{}, "kept"))
	ctx, stop := Detached(caller, 300<<20)

	defer stop()

	cancel()

	if ctx.Err() != nil {
		t.Fatal("caller cancellation reached the detached context")
	}

	if ctx.Value(ctxKey{}) != "kept" {
		t.Fatal("detached context lost the caller's values")
	}

	deadline, ok := ctx.Deadline()
	if want := TransferTimeout(300 << 20); !ok || time.Until(deadline) > want || time.Until(deadline) < want-time.Minute {
		t.Fatalf("deadline in %v, want about %v", time.Until(deadline), want)
	}
}

func TestTransferTimeoutScalesWithSize(t *testing.T) {
	t.Parallel()

	if got := TransferTimeout(-1); got != 2*time.Minute {
		t.Fatalf("negative size = %v", got)
	}

	if got := TransferTimeout(300 << 20); got != 2*time.Minute+1200*time.Second {
		t.Fatalf("300 MiB = %v", got)
	}
}
