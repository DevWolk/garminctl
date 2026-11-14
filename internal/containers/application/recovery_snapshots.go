package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// SnapshotFileRecord describes local backup bytes without exposing their contents.
type SnapshotFileRecord struct {
	Area, Path, Digest string
	Bytes              int64
}

// RecoverySnapshot is backup metadata, not a decision about data compatibility.
type RecoverySnapshot struct {
	ID              string
	CreatedAt       time.Time
	Current, Update PackageRecord
	Files           []SnapshotFileRecord
	MissingSources  []string
	Formats         *StateFormats
}

// RecoverySnapshots reads a trusted backup root and checks its local file hashes.
type RecoverySnapshots interface {
	List(context.Context) ([]RecoverySnapshot, error)
	Verify(context.Context, string) (*RecoverySnapshot, error)
}

// RecoverySnapshotsAction validates inspectable state without restoring it.
type RecoverySnapshotsAction struct{ store RecoverySnapshots }

// NewRecoverySnapshotsAction creates no account, backend, watch or storage owner.
func NewRecoverySnapshotsAction(store RecoverySnapshots) *RecoverySnapshotsAction {
	return &RecoverySnapshotsAction{store: store}
}

// List returns metadata only; it does not authenticate the retained file bytes.
func (a *RecoverySnapshotsAction) List(ctx context.Context) ([]RecoverySnapshot, error) {
	if err := a.check(ctx); err != nil {
		return nil, err
	}

	rows, err := a.store.List(ctx)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return nil, err
	}

	if len(rows) > 1000 {
		return nil, errors.New("recovery snapshot catalog exceeds the record limit")
	}

	result := make([]RecoverySnapshot, 0, len(rows))

	ids := make(map[string]bool, len(rows))
	for _, row := range rows {
		if err := validateRecoverySnapshot(row); err != nil {
			return nil, err
		}

		if ids[row.ID] {
			return nil, errors.New("recovery snapshot identity is ambiguous")
		}

		ids[row.ID] = true
		result = append(result, cloneRecoverySnapshot(row))
	}

	slices.SortFunc(result, func(a, b RecoverySnapshot) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}

		return strings.Compare(a.ID, b.ID)
	})

	return result, nil
}

// Verify checks one explicit snapshot without asserting compatibility or restoring files.
func (a *RecoverySnapshotsAction) Verify(ctx context.Context, id string) (*RecoverySnapshot, error) {
	if err := a.check(ctx); err != nil {
		return nil, err
	}

	if !validPackageHash(id) {
		return nil, errors.New("select a retained data snapshot ID")
	}

	value, err := a.store.Verify(ctx, id)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return nil, err
	}

	if value == nil || value.ID != id {
		return nil, errors.New("verified snapshot differs from the selected identity")
	}

	if err := validateRecoverySnapshot(*value); err != nil {
		return nil, err
	}

	result := cloneRecoverySnapshot(*value)

	return &result, nil
}

func (a *RecoverySnapshotsAction) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if a.store == nil {
		return errors.New("recovery snapshots are not configured")
	}

	return nil
}

func cloneRecoverySnapshot(value RecoverySnapshot) RecoverySnapshot {
	if value.Formats != nil {
		formats := *value.Formats
		value.Formats = &formats
	}

	value.Files = slices.Clone(value.Files)
	value.MissingSources = slices.Clone(value.MissingSources)

	return value
}

func validateRecoverySnapshot(value RecoverySnapshot) error {
	if !validPackageHash(value.ID) || value.CreatedAt.IsZero() || len(value.Files) > 4096 || len(value.MissingSources) > 2 {
		return errors.New("invalid recovery snapshot metadata")
	}

	target := DownloadTarget{OS: value.Current.OS, Arch: value.Current.Arch, Installation: value.Current.Installation}
	if err := validatePreflightPackage(value.Current, target, true); err != nil {
		return err
	}

	if err := validatePreflightPackage(value.Update, target, true); err != nil {
		return err
	}

	if !recoveryInstallation(value.Current) {
		return errors.New("snapshot has an unsupported installation target")
	}

	missing := make(map[string]bool, len(value.MissingSources))
	for _, area := range value.MissingSources {
		if (area != "config" && area != "cache") || missing[area] {
			return errors.New("snapshot has an unknown or duplicate missing source")
		}

		missing[area] = true
	}

	return validateSnapshotStateFiles(value.Files, missing)
}

func validateSnapshotStateFiles(files []SnapshotFileRecord, missing map[string]bool) error {
	seen := make(map[string]bool, len(files))

	var total int64

	for _, file := range files {
		if StatePath(file.Area, file.Path) != StateFile || !validPackageHash(file.Digest) || file.Bytes < 0 || file.Bytes > 64<<20 || missing[file.Area] {
			return errors.New("snapshot file is outside the selected local state contract")
		}

		key := strings.ToLower(file.Area + "/" + file.Path)
		if seen[key] {
			return errors.New("snapshot contains a duplicate state file")
		}

		seen[key] = true

		total += file.Bytes
		if total > 512<<20 {
			return errors.New("snapshot exceeds its total state limit")
		}
	}

	return nil
}
