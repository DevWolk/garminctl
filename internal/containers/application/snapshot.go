package application

import (
	"context"
	"errors"
)

// ErrPreparationBusy refuses a second use without cancelling the first operation.
var ErrPreparationBusy = errors.New("application preparation is already in use")

// DataSnapshot identifies one published copy; it grants no restore authority.
type DataSnapshot struct {
	ID, Directory string
	Files         int
	Bytes         int64
}

// DataSnapshots copies caller-owned local state without interpreting its contents.
type DataSnapshots interface {
	Create(context.Context, PreflightInfo) (*DataSnapshot, error)
}

// BackupDataAction takes a local snapshot while the existing preparation owns admission.
type BackupDataAction struct{ store DataSnapshots }

// NewBackupDataAction performs no IO.
func NewBackupDataAction(store DataSnapshots) *BackupDataAction {
	return &BackupDataAction{store: store}
}

// Run retains preparation on success and releases it on a copy failure.
func (a *BackupDataAction) Run(ctx context.Context, prepared *PreparedUpdate) (*DataSnapshot, error) {
	if prepared == nil {
		return nil, errors.New("application data backup requires an owned preparation")
	}

	var result *DataSnapshot

	err := prepared.withInfo(ctx, func(bound context.Context, info PreflightInfo) error {
		if a.store == nil {
			return errors.New("application data backup is not configured")
		}

		var err error

		result, err = a.store.Create(bound, info)
		if err != nil {
			return err
		}

		if result == nil || !validPackageHash(result.ID) || result.Files < 0 || result.Bytes < 0 {
			return errors.New("application data backup returned an invalid receipt")
		}

		return bound.Err()
	})
	if err != nil {
		if !errors.Is(err, ErrPreparationBusy) {
			err = errors.Join(err, prepared.Close())
		}

		return nil, err
	}

	return result, nil
}

func (p *PreparedUpdate) withInfo(ctx context.Context, use func(context.Context, PreflightInfo) error) error {
	if !p.mu.TryLock() {
		return ErrPreparationBusy
	}
	defer p.mu.Unlock()

	if p.lifetime == nil {
		return errors.New("application preparation has no lifetime")
	}

	bound, cancel := context.WithCancel(ctx)

	stop := context.AfterFunc(p.lifetime, cancel)
	defer func() { stop(); cancel() }()

	if err := p.lifetime.Err(); err != nil {
		return err
	}

	if err := bound.Err(); err != nil {
		return err
	}

	err := use(bound, p.info)

	return errors.Join(err, bound.Err(), p.lifetime.Err())
}
