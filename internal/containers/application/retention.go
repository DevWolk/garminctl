package application

import (
	"context"
	"errors"
	"slices"
)

// Retention policy for what an installation keeps for update and rollback.
// Older entries are removed when a newer one is saved; nothing is configurable.
const (
	// RetainedPackages is how many saved packages stay, counted from the most
	// recently saved. The package of the installed version always stays.
	RetainedPackages = 5
	// RetainedBackups is how many data backups stay, counted from the newest.
	RetainedBackups = 10
)

// UnusableEntry names retained storage this build cannot read or accept. It is
// shown so that it can be removed, never offered for recovery.
type UnusableEntry struct{ ID, Reason string }

// RecoveryCleanup is the optional part of a package or snapshot store that
// names the entries it skipped and deletes one entry by identity.
type RecoveryCleanup interface {
	Unusable(context.Context) ([]UnusableEntry, error)
	Remove(context.Context, string) error
}

func withStoredUnusable(ctx context.Context, store any, invalid []UnusableEntry) ([]UnusableEntry, error) {
	cleanup, ok := store.(RecoveryCleanup)
	if !ok {
		return invalid, nil
	}

	stored, err := cleanup.Unusable(ctx)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return nil, err
	}

	return append(slices.Clone(stored), invalid...), nil
}

func removeStored(ctx context.Context, store any, id string) error {
	cleanup, ok := store.(RecoveryCleanup)
	if !ok {
		return errors.New("this store cannot remove retained entries")
	}

	return errors.Join(cleanup.Remove(ctx, id), ctx.Err())
}
