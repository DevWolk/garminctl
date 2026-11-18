package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
	"github.com/DevWolk/garminctl/internal/ship/paths"
)

// restoreDataAction is an internal capability, never a native/HTTP/MCP method.
func (b *ApplicationBackend) restoreDataAction(address string, owned bool) (*application.RestoreDataAction, error) {
	account := gc.DefaultStorePath()
	if account == "" {
		return nil, errors.New("application data directory is unavailable")
	}

	config := filepath.Dir(account)
	directory := filepath.Join(config, "application-backups")
	reader := hostfile.NewSnapshotReader(directory)
	snapshots := application.NewRecoverySnapshotsAction(applicationRecoverySnapshots{reader: reader})
	store := applicationDataRestorations{backend: b, address: address, owned: owned, reader: reader, restorer: hostfile.NewSnapshotRestorer(directory, []hostfile.SnapshotSource{{Name: applicationStateConfig, Directory: config}, {Name: "cache", Directory: paths.Cache()}}, applicationSnapshotPath)}

	return application.NewRestoreDataAction(snapshots, store, b.joinedFor(address, owned)), nil
}

func (b *ApplicationBackend) joinedFor(address string, owned bool) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if b == nil || !owned {
			return errors.New("data restore requires the same owned stopped backend")
		}

		b.mu.Lock()
		defer b.mu.Unlock()

		if !b.started || b.address != address || b.done == nil {
			return errors.New("data restore does not belong to this backend")
		}

		select {
		case <-b.done:
			if b.runtime != nil || b.runErr != nil {
				return errors.Join(errors.New("owned backend did not join successfully"), b.runErr)
			}

			return nil
		default:
			return errors.New("stop and join the owned backend before restoring data")
		}
	}
}

type applicationDataRestorations struct {
	backend  *ApplicationBackend
	address  string
	owned    bool
	reader   *hostfile.SnapshotReader
	restorer *hostfile.SnapshotRestorer
}

func (s applicationDataRestorations) Restore(ctx context.Context, target, safety application.RecoverySnapshot) (*application.DataRestoreResult, error) {
	if s.backend == nil {
		return nil, errors.New("owned restore backend is unavailable")
	}

	if !s.backend.dataRestoreMu.TryLock() {
		return nil, application.ErrPreparationBusy
	}
	defer s.backend.dataRestoreMu.Unlock()

	if err := s.backend.joinedFor(s.address, s.owned)(ctx); err != nil {
		return nil, err
	}

	old, err := s.verifiedSnapshot(ctx, target)
	if err != nil {
		return nil, err
	}

	newer, err := s.verifiedSnapshot(ctx, safety)
	if err != nil {
		return nil, err
	}

	value, err := s.restorer.Restore(ctx, old, newer)

	result := &application.DataRestoreResult{TargetSnapshotID: target.ID, SafetySnapshotID: safety.ID}
	if value != nil {
		result.Attempted = value.Attempted
		result.Completed = value.Completed
		result.Replaced = value.Replaced
		result.Removed = value.Removed

		if value.Cleanup != nil {
			slog.Warn("restore staging directory was not removed", "error", value.Cleanup)
		}
	}

	return result, err
}

func (s applicationDataRestorations) verifiedSnapshot(ctx context.Context, expected application.RecoverySnapshot) (*hostfile.Snapshot, error) {
	value, err := s.reader.Open(ctx, expected.ID)
	if err != nil {
		return nil, err
	}

	actual, err := applicationRecoverySnapshot(*value)
	if err != nil {
		return nil, err
	}

	if !reflect.DeepEqual(*actual, expected) {
		return nil, errors.New("reviewed application backup changed before restoration")
	}

	return value, nil
}
