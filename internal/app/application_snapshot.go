package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/hostfile"
	"github.com/DevWolk/garminctl/internal/paths"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

type applicationDataSnapshots struct{ build string }

type applicationBackupMetadata struct {
	Current, Update application.PackageRecord
	Formats         *application.StateFormats `json:",omitempty"`
}

func (s applicationDataSnapshots) Create(ctx context.Context, info application.PreflightInfo) (*application.DataSnapshot, error) {
	account := gc.DefaultStorePath()
	if account == "" {
		return nil, errors.New("application data directory is unavailable")
	}

	config := filepath.Dir(account)

	value := applicationBackupMetadata{Current: info.Current, Update: info.Update}
	if s.build != "" && s.build == info.Current.Tag && s.build == info.Inventory.CLI.Version {
		formats := application.CurrentStateFormats()
		value.Formats = &formats
	}

	metadata, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	store := hostfile.NewSnapshotStore(filepath.Join(config, "application-backups"), []hostfile.SnapshotSource{
		{Name: applicationStateConfig, Directory: config}, {Name: "cache", Directory: paths.Cache()},
	}, applicationSnapshotPath)

	result, err := store.Create(ctx, metadata)
	if err != nil {
		return nil, err
	}

	var bytes int64
	for _, file := range result.Files {
		bytes += file.Bytes
	}

	return &application.DataSnapshot{ID: result.ID, Directory: result.Directory, Files: len(result.Files), Bytes: bytes}, nil
}

func applicationSnapshotPath(area, relative string) hostfile.SnapshotKind {
	switch application.StatePath(area, relative) {
	case application.StateFile:
		return hostfile.SnapshotFile
	case application.StateDirectory:
		return hostfile.SnapshotDirectory
	default:
		return hostfile.SnapshotIgnore
	}
}

func (b *ApplicationBackend) snapshotContextFor(address string, owned bool) func(context.Context) (context.Context, context.CancelFunc, error) {
	return func(ctx context.Context) (context.Context, context.CancelFunc, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		if !owned {
			return nil, nil, errors.New("application data backup requires the owned backend")
		}

		runtime, err := b.current(address)
		if err != nil {
			return nil, nil, err
		}

		if runtime.Manager().Gate().Snapshot().State != lifecycle.Maintenance {
			return nil, nil, errors.New("application data backup requires active maintenance")
		}

		bound, cancel := runtime.RequestContext(ctx)

		return bound, cancel, nil
	}
}

// BackupData is internal to the owned preparation and installation pipeline.
func (r *applicationNativeReader) BackupData(ctx context.Context, prepared *application.PreparedUpdate) (*application.DataSnapshot, error) {
	if r.snapshotContext == nil || r.snapshot == nil {
		return nil, abortApplicationSnapshot(prepared, errors.New("owned application data backup is not configured"))
	}

	bound, cancel, err := r.snapshotContext(ctx)
	if err != nil {
		return nil, abortApplicationSnapshot(prepared, err)
	}
	defer cancel()

	return r.snapshot.Run(bound, prepared)
}

func abortApplicationSnapshot(prepared *application.PreparedUpdate, err error) error {
	if prepared != nil {
		return errors.Join(err, prepared.Close())
	}

	return err
}
