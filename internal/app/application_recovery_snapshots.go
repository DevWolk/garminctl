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

	reader := hostfile.NewSnapshotReader(filepath.Join(filepath.Dir(account), "application-backups"))

	return application.NewRecoverySnapshotsAction(applicationRecoverySnapshots{reader: reader}), nil
}

type applicationRecoverySnapshots struct{ reader *hostfile.SnapshotReader }

func (s applicationRecoverySnapshots) List(ctx context.Context) ([]application.RecoverySnapshot, error) {
	rows, err := s.reader.List(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]application.RecoverySnapshot, 0, len(rows))
	for _, row := range rows {
		value, err := applicationRecoverySnapshot(row)
		if err != nil {
			return nil, err
		}

		result = append(result, *value)
	}

	return result, nil
}

func (s applicationRecoverySnapshots) Verify(ctx context.Context, id string) (*application.RecoverySnapshot, error) {
	value, err := s.reader.Open(ctx, id)
	if err != nil {
		return nil, err
	}

	return applicationRecoverySnapshot(*value)
}

func applicationRecoverySnapshot(value hostfile.Snapshot) (*application.RecoverySnapshot, error) {
	var metadata struct{ Current, Update application.PackageRecord }

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

	return &application.RecoverySnapshot{ID: value.ID, CreatedAt: value.CreatedAt, Current: metadata.Current, Update: metadata.Update, Files: files, MissingSources: append([]string(nil), value.MissingSources...)}, nil
}
