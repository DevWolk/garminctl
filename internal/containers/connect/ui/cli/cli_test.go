package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/connect"
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

// stuckInput is a terminal-like input whose Close does not end a pending read.
type stuckInput struct{ release chan struct{} }

func (s stuckInput) Read([]byte) (int, error) {
	<-s.release

	return 0, io.EOF
}

func (stuckInput) Close() error { return nil }

func TestVerificationPromptCancellationDoesNotWaitForInput(t *testing.T) {
	t.Parallel()

	input := stuckInput{release: make(chan struct{})}

	t.Cleanup(func() { close(input.release) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan error, 1)

	go func() {
		_, err := readVerificationCode(ctx, input)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled prompt = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled prompt waited for a line of input")
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

func TestCloudSyncFailsWhenSomethingWasNotSynced(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		res    connect.SyncResult
		pulled string
		fails  bool
	}{
		{name: "complete", res: connect.SyncResult{Upload: &connect.UploadResult{Uploaded: 1}, Pulled: []connect.PulledFile{{Name: "course", Path: "GARMIN/NewFiles/a.fit"}}}, pulled: "pulled 1 item(s)"},
		{name: "failed upload", res: connect.SyncResult{Upload: &connect.UploadResult{Uploaded: 1, Failed: 1}}, pulled: "pulled 0 item(s)", fails: true},
		{name: "failed delivery", res: connect.SyncResult{Pulled: []connect.PulledFile{{Name: "course", Path: "GARMIN/NewFiles/a.fit"}, {Name: "workout", Error: "watch full"}}}, pulled: "pulled 1 item(s)", fails: true},
		{name: "download error", res: connect.SyncResult{Errors: []string{"download: queue unavailable"}}, pulled: "pulled 0 item(s)", fails: true},
	} {
		for _, asJSON := range []bool{false, true} {
			var out bytes.Buffer

			command := &Command{Out: clix.NewPrinter(&out), ErrOut: clix.NewPrinter(io.Discard), JSON: asJSON, RunSync: func(context.Context, bool, bool, connect.Progress) (*connect.SyncResult, error) {
				res := tt.res
				return &res, nil
			}}

			err := command.Run(t.Context(), []string{"sync"})
			if (err != nil) != tt.fails {
				t.Fatalf("%s json=%v: error=%v", tt.name, asJSON, err)
			}

			if !asJSON && !strings.Contains(out.String(), tt.pulled) {
				t.Fatalf("%s: output %q, want %q", tt.name, &out, tt.pulled)
			}

			if asJSON && !strings.Contains(out.String(), `"pulled"`) {
				t.Fatalf("%s: JSON output %q", tt.name, &out)
			}
		}
	}
}
