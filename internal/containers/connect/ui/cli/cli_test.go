package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/gc"
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

func TestLogoutCommandDeletesSessionCopiesBeforeReportingSuccess(t *testing.T) {
	t.Parallel()

	for _, failure := range []error{nil, errors.New("fixture backup is read-only")} {
		var out bytes.Buffer

		calls := 0
		command := &Command{Out: clix.NewPrinter(&out), ErrOut: clix.NewPrinter(io.Discard), Client: gc.New(filepath.Join(t.TempDir(), "garmin-connect.json")), ForgetSessionCopies: func(context.Context) error {
			calls++

			return failure
		}}

		err := command.Account(t.Context(), []string{"logout"})
		if !errors.Is(err, failure) || calls != 1 || (failure == nil) != strings.Contains(out.String(), "signed out") {
			t.Fatalf("failure=%v: error=%v calls=%d out=%q", failure, err, calls, &out)
		}
	}
}
