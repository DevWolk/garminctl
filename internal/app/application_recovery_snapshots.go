package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/hostfile"
)

// NewApplicationRecoverySnapshots binds only the configured protected backup root.
// It neither reads the account nor creates a backend, native host or watch owner.
func NewApplicationRecoverySnapshots() (*application.RecoverySnapshotsAction, error) {
	account := gc.DefaultStorePath()
	if account == "" {
		return nil, errors.New("application data directory is unavailable")
	}

	reader := hostfile.NewSnapshotReader(filepath.Join(filepath.Dir(account), applicationBackups))

	return application.NewRecoverySnapshotsAction(applicationRecoverySnapshots{reader: reader}), nil
}

type applicationRecoverySnapshots struct{ reader *hostfile.SnapshotReader }

func (s applicationRecoverySnapshots) List(ctx context.Context) ([]application.RecoverySnapshot, error) {
	result, _, err := s.catalog(ctx)

	return result, err
}

// Unusable names the backups List left out: a manifest or application metadata
// this build cannot read, such as a backup written by a later build.
func (s applicationRecoverySnapshots) Unusable(ctx context.Context) ([]application.UnusableEntry, error) {
	_, skipped, err := s.catalog(ctx)

	return skipped, err
}

func (s applicationRecoverySnapshots) Remove(ctx context.Context, id string) error {
	return s.reader.Remove(ctx, id)
}

func (s applicationRecoverySnapshots) catalog(ctx context.Context) ([]application.RecoverySnapshot, []application.UnusableEntry, error) {
	rows, unreadable, err := s.reader.Catalog(ctx)
	if err != nil {
		return nil, nil, err
	}

	result, skipped := make([]application.RecoverySnapshot, 0, len(rows)), make([]application.UnusableEntry, 0, len(unreadable))
	for _, entry := range unreadable {
		skipped = append(skipped, application.UnusableEntry{ID: entry.ID, Reason: entry.Reason})
	}

	for _, row := range rows {
		value, err := applicationRecoverySnapshot(row)
		if err != nil {
			skipped = append(skipped, application.UnusableEntry{ID: row.ID, Reason: "application backup metadata: " + err.Error()})

			continue
		}

		result = append(result, *value)
	}

	return result, skipped, nil
}

func (s applicationRecoverySnapshots) Verify(ctx context.Context, id string) (*application.RecoverySnapshot, error) {
	value, err := s.reader.Open(ctx, id)
	if err != nil {
		return nil, err
	}

	return applicationRecoverySnapshot(*value)
}

func applicationRecoverySnapshot(value hostfile.Snapshot) (*application.RecoverySnapshot, error) {
	var metadata applicationBackupMetadata

	decoder := json.NewDecoder(bytes.NewReader(value.Metadata))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&metadata); err != nil {
		return nil, err
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("unexpected trailing application backup metadata")
	}

	files := make([]application.SnapshotFileRecord, 0, len(value.Files))
	for _, file := range value.Files {
		files = append(files, application.SnapshotFileRecord{Area: file.Area, Path: file.Path, Digest: file.Digest, Bytes: file.Bytes})
	}

	return &application.RecoverySnapshot{ID: value.ID, CreatedAt: value.CreatedAt, Current: metadata.Current, Update: metadata.Update, Files: files, MissingSources: append([]string(nil), value.MissingSources...), Formats: metadata.Formats}, nil
}
