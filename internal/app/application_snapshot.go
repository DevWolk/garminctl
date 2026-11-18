package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/paths"
)

// applicationBackups names the data backup directory beside the account store.
const applicationBackups = "application-backups"

type applicationDataSnapshots struct{ build string }

// ForgetApplicationSessionCopies deletes the saved Connect session and pending
// verification state from every application data backup, so that signing out
// leaves no copy a later restore could bring back. The backups stay usable.
func ForgetApplicationSessionCopies(ctx context.Context) error {
	account := gc.DefaultStorePath()
	if account == "" {
		return errors.New("application data directory is unavailable")
	}

	name := filepath.Base(account)

	return hostfile.NewSnapshotReader(filepath.Join(filepath.Dir(account), applicationBackups)).Forget(ctx, applicationStateConfig, name, name+".mfa")
}

// pruneApplicationBackups keeps the newest backups once a forward update was
// handed to the installer. Pruning earlier would let each refused or cancelled
// attempt evict a backup that an older version's rollback needs. A leftover that
// cannot be removed now must not fail the update.
func pruneApplicationBackups(ctx context.Context) {
	account := gc.DefaultStorePath()
	if account == "" {
		return
	}

	if err := hostfile.NewSnapshotReader(filepath.Join(filepath.Dir(account), applicationBackups)).Prune(ctx, application.RetainedBackups); err != nil {
		slog.Warn("application backup retention left files behind", "error", err)
	}
}

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

	backups := filepath.Join(config, applicationBackups)
	store := hostfile.NewSnapshotStore(backups, []hostfile.SnapshotSource{
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
