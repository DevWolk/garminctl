package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

func TestStatusCLIPrintsTheLastInstallationResult(t *testing.T) {
	t.Parallel()

	failed := &application.InstallationOutcome{Tag: "v1.2.3", Status: application.InstallationFailed, Error: "exit status 126", Finished: time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC), Directory: "/cache/stage-1"}

	for _, tc := range []struct {
		name    string
		outcome *application.InstallationOutcome
		want    string
	}{
		{"failed", failed, failed.Summary()},
		{"nothing recorded", nil, "No installation started from the application has been recorded."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out, diagnostics bytes.Buffer

			command := &Command{Out: clix.NewPrinter(&out), ErrOut: clix.NewPrinter(&diagnostics), LastInstallation: func(context.Context) (*application.InstallationOutcome, error) {
				return tc.outcome, nil
			}, Recovery: func() (*application.RecoveryAction, error) {
				t.Fatal("status opened the package cache")
				return nil, nil
			}}

			if err := command.Run(t.Context(), []string{statusCommand}); err != nil {
				t.Fatal(err)
			}

			if strings.TrimSpace(out.String()) != tc.want {
				t.Fatalf("status output=%q", out.String())
			}

			out.Reset()

			command.JSON = true
			if err := command.Run(t.Context(), []string{statusCommand}); err != nil {
				t.Fatal(err)
			}

			var value struct {
				LastInstallation *application.InstallationOutcome `json:"lastInstallation"`
			}
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatalf("status JSON %q: %v", out.String(), err)
			}

			if (value.LastInstallation == nil) != (tc.outcome == nil) || (tc.outcome != nil && *value.LastInstallation != *tc.outcome) {
				t.Fatalf("status JSON=%+v", value.LastInstallation)
			}
		})
	}
}

func TestStatusCLIReportsUnreadableRecordAndRejectsArguments(t *testing.T) {
	t.Parallel()

	failure := errors.New("record is damaged")
	command := &Command{Out: clix.NewPrinter(new(bytes.Buffer)), ErrOut: clix.NewPrinter(new(bytes.Buffer)), LastInstallation: func(context.Context) (*application.InstallationOutcome, error) {
		return nil, failure
	}}

	if err := command.Run(t.Context(), []string{statusCommand}); !errors.Is(err, failure) {
		t.Fatalf("unreadable record=%v", err)
	}

	if err := command.Run(t.Context(), []string{statusCommand, "unexpected"}); clix.ExitCode(err) != 2 {
		t.Fatalf("status with an argument=%v", err)
	}

	command.LastInstallation = nil
	if err := command.Run(t.Context(), []string{statusCommand}); err == nil {
		t.Fatal("unconfigured status succeeded")
	}
}
