package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// RollbackOption is retained display metadata, not verified bytes or compatibility.
type RollbackOption struct {
	Package    PackageRecord
	SnapshotID string
	CreatedAt  time.Time
	Files      int
	Bytes      int64
}

// Options pairs older package metadata with its explicit local data snapshots.
// It never takes maintenance, verifies package bytes or starts preparation.
func (a *RollbackAction) Options(ctx context.Context, prerelease bool) ([]RollbackOption, error) {
	if a.preflight == nil || a.restore == nil || a.restore.snapshots == nil {
		return nil, errors.New("rollback metadata is unavailable")
	}

	packages, err := a.preflight.retained(ctx, prerelease, true)
	if err != nil {
		return nil, err
	}

	snapshots, err := a.restore.snapshots.List(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := pairRollbackOptions(packages, snapshots)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(rows, func(a, b RollbackOption) int {
		if order := semver.Compare(semanticVersion(b.Package.Tag), semanticVersion(a.Package.Tag)); order != 0 {
			return order
		}

		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}

		return strings.Compare(a.SnapshotID, b.SnapshotID)
	})

	return rows, ctx.Err()
}

func pairRollbackOptions(packages []PackageRecord, snapshots []RecoverySnapshot) ([]RollbackOption, error) {
	byID := make(map[string]PackageRecord, len(packages))
	for _, p := range packages {
		if _, ok := byID[p.ID]; ok {
			return nil, errors.New("rollback package metadata is ambiguous")
		}

		byID[p.ID] = p
	}

	rows := make([]RollbackOption, 0)

	for _, snapshot := range snapshots {
		p, ok := byID[snapshot.Current.ID]
		if !ok || p != snapshot.Current {
			continue
		}

		if len(rows) == 1000 {
			return nil, errors.New("rollback choices exceed the record limit")
		}

		var bytes int64
		for _, file := range snapshot.Files {
			bytes += file.Bytes
		}

		rows = append(rows, RollbackOption{Package: p, SnapshotID: snapshot.ID, CreatedAt: snapshot.CreatedAt, Files: len(snapshot.Files), Bytes: bytes})
	}

	return rows, nil
}
