package application_test

import (
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

func TestInstallationOutcomeSummaryNamesReleaseReasonAndLogs(t *testing.T) {
	t.Parallel()

	const (
		tag  = "v1.3.9"
		logs = "/cache/stage-1"
	)

	finished := time.Date(2026, 3, 4, 5, 6, 0, 0, time.Local)

	for _, tc := range []struct {
		name       string
		outcome    application.InstallationOutcome
		incomplete bool
		want       []string
	}{
		{"installed", application.InstallationOutcome{Tag: tag, Status: application.InstallationInstalled, Finished: finished, Directory: logs}, false, []string{"installed " + tag, "2026-03-04 05:06"}},
		{"failed", application.InstallationOutcome{Tag: tag, Status: application.InstallationFailed, Error: "exit status 126", Finished: finished, Directory: logs}, true, []string{tag, "did not complete: exit status 126", logs}},
		{"a failure after the programs were replaced", application.InstallationOutcome{Tag: tag, Status: application.InstallationFailed, Error: "exit status 1\ncontext deadline exceeded", Finished: finished, Directory: logs}, true, []string{"did not complete: exit status 1; context deadline exceeded.", "check the installed versions", logs}},
		{"failed without a reason", application.InstallationOutcome{Tag: tag, Status: application.InstallationFailed, Finished: finished}, true, []string{"no reason was recorded"}},
		{"still running", application.InstallationOutcome{Tag: tag, Status: application.InstallationUnknown, Finished: finished, Directory: logs}, true, []string{"still installing", "check the installed version", logs}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			summary := tc.outcome.Summary()
			for _, part := range tc.want {
				if !strings.Contains(summary, part) {
					t.Fatalf("summary %q lacks %q", summary, part)
				}
			}

			if tc.outcome.Incomplete() != tc.incomplete || strings.Contains(summary, "\n") {
				t.Fatalf("incomplete=%t summary=%q", tc.outcome.Incomplete(), summary)
			}
		})
	}
}

func TestInstallationOutcomeIsSupersededByTheSameOrALaterVersion(t *testing.T) {
	t.Parallel()

	failed := application.InstallationOutcome{Tag: "v0.6.2", Status: application.InstallationFailed}
	for _, tt := range []struct {
		running    string
		superseded bool
	}{
		{"0.6.2", true},
		{"v0.6.3", true},
		{"0.6.1", false},
		{"dev", false},
		{"", false},
	} {
		if got := failed.Superseded(tt.running); got != tt.superseded {
			t.Errorf("Superseded(%q) = %t, want %t", tt.running, got, tt.superseded)
		}
	}
}
