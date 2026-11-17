package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

const (
	stateConfigArea = "config"
	stateCacheArea  = "cache"
)

type dataSnapshotStore struct {
	create func(context.Context, application.PreflightInfo) (*application.DataSnapshot, error)
}

func (s dataSnapshotStore) Create(ctx context.Context, info application.PreflightInfo) (*application.DataSnapshot, error) {
	return s.create(ctx, info)
}

func TestBackupDataKeepsPreparationOnSuccessAndReleasesOnFailure(t *testing.T) {
	t.Parallel()

	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inventory, packages, request := preflightFixture()
			gate := new(lifecycle.Gate)

			prepared, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}

			defer func() {
				if err := prepared.Close(); err != nil {
					t.Error(err)
				}
			}()

			failure := errors.New("fixture disk full")
			store := dataSnapshotStore{create: func(_ context.Context, info application.PreflightInfo) (*application.DataSnapshot, error) {
				if info != prepared.Info() || gate.Snapshot().State != lifecycle.Maintenance {
					t.Fatal("snapshot lost preparation identity or admission")
				}

				if fail {
					return nil, failure
				}

				return &application.DataSnapshot{ID: strings.Repeat("e", 64), Files: 2, Bytes: 42}, nil
			}}

			got, err := application.NewBackupDataAction(store).Run(t.Context(), prepared)
			if fail {
				if got != nil || !errors.Is(err, failure) {
					t.Fatalf("failure=%v %v", got, err)
				}

				assertPreflightReleased(t, gate, packages)
			} else if err != nil || got == nil || got.Files != 2 || gate.Snapshot().State != lifecycle.Maintenance {
				t.Fatalf("snapshot=%v error=%v state=%v", got, err, gate.Snapshot())
			}
		})
	}
}

func TestBackupDataCloseCancelsAndJoinsCopy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		inventory, packages, request := preflightFixture()
		gate := new(lifecycle.Gate)

		prepared, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}

		started, cancelled, unblock, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan error, 1)

		finishCopy := sync.OnceFunc(func() { close(unblock) })
		defer finishCopy()

		store := dataSnapshotStore{create: func(ctx context.Context, _ application.PreflightInfo) (*application.DataSnapshot, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-unblock

			return nil, ctx.Err()
		}}
		go func() { _, err := application.NewBackupDataAction(store).Run(t.Context(), prepared); finished <- err }()

		synctest.Wait()

		select {
		case <-started:
		default:
			t.Fatal("copy did not start")
		}

		closed := make(chan error, 1)
		go func() { closed <- prepared.Close() }()

		<-cancelled

		if gate.Snapshot().State != lifecycle.Maintenance {
			t.Fatal("Close released admission before copy joined")
		}

		select {
		case err := <-closed:
			t.Fatalf("Close returned before copy joined: %v", err)
		default:
		}

		finishCopy()

		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("copy=%v", err)
		}

		if err := <-closed; err != nil {
			t.Fatal(err)
		}

		assertPreflightReleased(t, gate, packages)
	})
}

func TestBackupDataRejectsMissingClosedAndCancelledPreparation(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"missing", "closed", "caller cancellation", "unconfigured"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inventory, packages, request := preflightFixture()
			gate := new(lifecycle.Gate)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			prepared, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(ctx, request)
			if err != nil {
				t.Fatal(err)
			}

			defer func() {
				if err := prepared.Close(); err != nil {
					t.Error(err)
				}
			}()

			var store application.DataSnapshots = dataSnapshotStore{create: func(context.Context, application.PreflightInfo) (*application.DataSnapshot, error) {
				t.Fatal("invalid preparation reached data IO")
				return nil, nil
			}}

			selected := prepared

			switch name {
			case "missing":
				selected = nil
			case "closed":
				if err := prepared.Close(); err != nil {
					t.Fatal(err)
				}
			case "caller cancellation":
				cancel()
			case "unconfigured":
				store = nil
			}

			if got, err := application.NewBackupDataAction(store).Run(t.Context(), selected); got != nil || err == nil {
				t.Fatalf("accepted=%v %v", got, err)
			}
		})
	}
}

func TestStatePathSelectsOnlyAuthoritativeLocalData(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		area, path string
		want       application.StatePathKind
	}{
		{stateConfigArea, "garmin-connect.json", application.StateFile},
		{stateConfigArea, "garmin-connect.json.mfa", application.StateFile},
		{stateCacheArea, "uploaded-123.json", application.StateFile},
		{stateCacheArea, "OUT.BIN-20261112-090001-123456", application.StateDirectory},
		{stateCacheArea, "OUT.BIN-20261112-090001-123456/OUT.BIN", application.StateFile},
		{stateCacheArea, "OUT.BIN-20261112-090001-123456/IN.BIN", application.StateFile},
		{stateCacheArea, "FACE0001.SET-20261112-090001-123456/FACE0001.SET", application.StateFile},
		{stateCacheArea, "FACE0001.SET-20261112-090001-123456/other.SET", application.StateIgnore},
		{stateCacheArea, "OUT.BIN-20261112-090001-123456/extra.json", application.StateIgnore},
		{stateCacheArea, "activities-123.json", application.StateIgnore},
		{stateCacheArea, "application-packages", application.StateIgnore},
		{stateConfigArea, "desktop", application.StateIgnore},
		{stateConfigArea, "application-backups", application.StateIgnore},
		{stateCacheArea, "uploaded-../evil.json", application.StateIgnore},
	} {
		t.Run(tc.area+"/"+tc.path, func(t *testing.T) {
			t.Parallel()

			if got := application.StatePath(tc.area, tc.path); got != tc.want {
				t.Fatalf("kind=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestBackupDataConcurrentRequestDoesNotCancelActiveCopy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		inventory, packages, request := preflightFixture()
		gate := new(lifecycle.Gate)

		prepared, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}

		defer func() {
			if err := prepared.Close(); err != nil {
				t.Error(err)
			}
		}()

		started, unblock, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		store := dataSnapshotStore{create: func(ctx context.Context, _ application.PreflightInfo) (*application.DataSnapshot, error) {
			close(started)
			<-unblock

			return &application.DataSnapshot{ID: strings.Repeat("e", 64)}, ctx.Err()
		}}

		action := application.NewBackupDataAction(store)
		go func() { _, err := action.Run(t.Context(), prepared); done <- err }()

		synctest.Wait()

		select {
		case <-started:
		default:
			t.Fatal("copy did not start")
		}

		if result, err := action.Run(t.Context(), prepared); result != nil || !errors.Is(err, application.ErrPreparationBusy) {
			t.Fatalf("contender=%v %v", result, err)
		}

		close(unblock)

		if err := <-done; err != nil {
			t.Fatalf("second request cancelled the active copy: %v", err)
		}

		if gate.Snapshot().State != lifecycle.Maintenance {
			t.Fatal("busy refusal released another operation's ticket")
		}
	})
}

func TestBackupDataPortBusyErrorReleasesItsOwnPreparation(t *testing.T) {
	t.Parallel()

	inventory, packages, request := preflightFixture()
	gate := new(lifecycle.Gate)

	prepared, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := prepared.Close(); err != nil {
			t.Error(err)
		}
	}()

	store := dataSnapshotStore{create: func(context.Context, application.PreflightInfo) (*application.DataSnapshot, error) {
		return nil, application.ErrPreparationBusy
	}}

	result, err := application.NewBackupDataAction(store).Run(t.Context(), prepared)
	if result != nil || !errors.Is(err, application.ErrPreparationBusy) {
		t.Fatalf("port refusal=%+v %v", result, err)
	}

	assertPreflightReleased(t, gate, packages)
}
