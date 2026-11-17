//go:build unix

package updatetransport

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A helper's stdin is a blocking pipe: closing it does not wake a reader, so
// the deadline itself has to end the wait.
func TestInstallDecisionDeadlineEndsWaitOnSilentBlockingPipe(t *testing.T) {
	t.Parallel()

	var descriptors [2]int
	if err := syscall.Pipe(descriptors[:]); err != nil {
		t.Fatal(err)
	}

	input := os.NewFile(uintptr(descriptors[0]), "helper stdin")
	owner := os.NewFile(uintptr(descriptors[1]), "owner pipe")

	t.Cleanup(func() { _ = owner.Close(); _ = input.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := readInstallDecision(ctx, bufio.NewReaderSize(input, maxStageRequest+1), input, strings.Repeat("a", 64))
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("decision wait ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the decision wait outlived its deadline while the owner kept the pipe open")
	}
}
