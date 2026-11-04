package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestVerificationPromptCancellation(t *testing.T) {
	t.Parallel()

	input, output := io.Pipe()

	t.Cleanup(func() { _ = output.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := readVerificationCode(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prompt = %v", err)
	}

	if _, err := output.Write([]byte("not required to unblock cancellation\n")); err == nil {
		t.Fatal("cancelled prompt retained its input reader")
	}
}

func TestVerificationPromptEOF(t *testing.T) {
	t.Parallel()

	code, err := readVerificationCode(t.Context(), io.NopCloser(strings.NewReader("")))
	if err != nil || code != "" {
		t.Fatalf("EOF = %q, %v", code, err)
	}
}
