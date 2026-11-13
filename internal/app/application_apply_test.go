package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

func TestApplicationNativeInstallVerifiesBacksUpAndJoinsBeforeFakeCommit(t *testing.T) { //nolint:paralleltest // Production composition uses isolated account/cache paths and inert signed package fixtures.
	f := applicationPreflightFixture(t)
	commits := 0
	installer := applicationFixtureInstaller{prepare: func(_ context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
		if input.Snapshot.ID == "" || input.Update == nil || input.Current == nil {
			return nil, errors.New("missing owned verified preparation")
		}

		return applicationFixturePendingInstaller{commit: func(context.Context) error {
			commits++

			if f.backend.runtime.Manager().Gate().Snapshot().State != lifecycle.Closed {
				return errors.New("backend not joined before commit")
			}

			return nil
		}, close: func() error { return nil }}, nil
	}}
	f.reader.handoff = application.NewInstallHandoffAction(f.reader.ApplicationReads, installer, f.backend.control.stopFor(f.backend.address, true))

	var phases []string

	result, err := f.reader.Install(t.Context(), f.request(), application.InstallConsent{Confirmed: true, EditsResolved: true}, func(phase string) { phases = append(phases, phase) })
	if err != nil || result == nil || result.State != application.HandoffAccepted || commits != 1 || !slices.Equal(phases, []string{"verifying", "backing-up", "handoff"}) {
		t.Fatalf("result=%+v %v phases=%v commits=%d", result, err, phases, commits)
	}

	assertDownloadOperationsClean(t, f.pipeline)

	if f.external.Load() != 0 {
		t.Fatal("offline installation preparation contacted release feed")
	}
}

func TestApplicationNativeInstallRefusesConsentBeforeIO(t *testing.T) {
	t.Parallel()

	reader := new(applicationNativeReader)
	if value, err := reader.Install(t.Context(), application.PreflightRequest{}, application.InstallConsent{}, nil); err == nil || value != nil {
		t.Fatalf("unconfirmed install=%+v %v", value, err)
	}
}
