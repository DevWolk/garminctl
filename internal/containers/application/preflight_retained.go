package application

import (
	"context"
	"errors"
	"slices"

	"golang.org/x/mod/semver"
)

// RetainedUpdates lists eligible display metadata without maintenance or opening bytes.
// Run still rechecks inventory, verifies both packages and requires recovery.
func (a *PreflightAction) RetainedUpdates(ctx context.Context, prerelease bool) ([]PackageRecord, error) {
	if a.inventory == nil || a.packages == nil {
		return nil, errors.New("application package inventory is unavailable")
	}

	info, target, err := a.readPreflightInventory(ctx)
	if err != nil {
		return nil, err
	}

	if err := validateHandoffTarget(info); err != nil {
		return nil, err
	}

	rows, err := a.packages.List(ctx, target)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return nil, err
	}

	if len(rows) > 1000 {
		return nil, errors.New("package catalog exceeds the record limit")
	}

	result := make([]PackageRecord, 0, len(rows))
	for _, row := range rows {
		if validatePreflightPackage(row, target, prerelease) != nil || semver.Compare(semanticVersion(row.Tag), semanticVersion(target.CLIVersion)) <= 0 {
			continue
		}

		result = append(result, row)
	}

	slices.SortFunc(result, func(a, b PackageRecord) int { return semver.Compare(semanticVersion(b.Tag), semanticVersion(a.Tag)) })

	return result, nil
}
