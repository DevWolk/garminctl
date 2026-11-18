package app

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

func (r *applicationNativeReader) RetainedUpdates(ctx context.Context, prerelease bool) ([]application.PackageRecord, error) {
	if r.preflight == nil {
		return nil, errors.New(r.disabled)
	}

	return r.preflight.RetainedUpdates(ctx, prerelease)
}

// Install combines existing owned preparation and handoff without fetching releases.
func (r *applicationNativeReader) Install(ctx context.Context, request application.PreflightRequest, consent application.InstallConsent, progress func(string)) (result *application.InstallHandoffResult, err error) {
	if !consent.Confirmed || !consent.EditsResolved {
		return nil, errors.New("confirm installation and resolve unsaved edits first")
	}

	phase := func(value string) {
		if progress != nil {
			progress(value)
		}
	}
	phase("verifying")

	prepared, err := r.Preflight(ctx, request)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, prepared.Close()) }()

	phase("backing-up")

	if _, err := r.BackupData(ctx, prepared); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	phase("handoff")

	result, err = r.Handoff(ctx, prepared, consent)
	// A rollback restores an older backup chosen before its safety copy was
	// made, so only an accepted forward update removes old backups.
	if err == nil && !prepared.Info().Downgrade() {
		pruneApplicationBackups(ctx)
	}

	return result, err
}
