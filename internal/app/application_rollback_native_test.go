package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

func TestNativeRollbackAdapterKeepsOneOwnedPreparationThroughConfirmation(t *testing.T) { //nolint:paralleltest // Fixture backend and cached CLI isolate process account/cache paths.
	f := newApplicationRollbackFixture(t)
	f.action(t, false)

	rows, err := f.f.reader.RetainedRollbacks(t.Context(), false)
	if err != nil || len(rows) != 1 || rows[0].SnapshotID != f.old.ID {
		t.Fatalf("native options=%+v %v", rows, err)
	}

	prepared, err := f.f.reader.PrepareRollback(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}

	closeRestoreResource(t, prepared)

	view := prepared.View()
	if f.commits != 0 || view.Data.TargetSnapshotID != f.old.ID || view.Data.SafetySnapshotID == "" {
		t.Fatalf("preparation=%+v commits=%d", view, f.commits)
	}

	if got := readApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account)); string(got) != `{"opaque":"edited after upgrade"}` {
		t.Fatal("review replaced live data")
	}

	result, err := prepared.Run(t.Context(), application.RollbackConsent{Installation: application.InstallConsent{Confirmed: true, EditsResolved: true}, Data: application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: view.Data.ReviewID}})
	assertApplicationRollback(t, f, result, err, false)
}

// rollbackTestWindow lends the native service a window lifetime the test can end.
type rollbackTestWindow struct {
	current func() context.Context
	address string
	active  atomic.Int32
	holds   atomic.Int32
	exited  chan struct{}
}

func (w *rollbackTestWindow) Acquire() (context.Context, func(), error) {
	w.active.Add(1)

	return w.current(), sync.OnceFunc(func() { w.active.Add(-1) }), nil
}

func (w *rollbackTestWindow) Backend() (string, bool, error) { return w.address, true, nil }

func (w *rollbackTestWindow) ExitForUpdate() { close(w.exited) }

func (w *rollbackTestWindow) HoldClose() (func(), error) {
	w.holds.Add(1)

	return sync.OnceFunc(func() { w.holds.Add(-1) }), nil
}

func TestNativeRollbackRestoresAndCommitsAfterTheWindowLifetimeEndsMidHandoff(t *testing.T) { //nolint:paralleltest // Fixture backend and cached CLI isolate process account/cache paths.
	f := newApplicationRollbackFixture(t)
	f.action(t, false)

	lifetime, closeWindow := context.WithCancel(t.Context())
	defer closeWindow()

	window := &rollbackTestWindow{current: func() context.Context { return lifetime }, address: f.f.backend.address, exited: make(chan struct{})}
	// The window lifetime ends after confirmation, before the owned stop and the restore.
	f.payload = closeWindow
	service := applicationnative.NewService(window, func(string, bool) (applicationnative.Reader, error) { return f.f.reader, nil })

	started, err := service.StartRollback(f.request)
	if err != nil {
		t.Fatal(err)
	}

	review := waitNativeRollbackState(t, service, func(state *applicationnative.InstallationState) bool { return state.Phase == "review" || !state.Active })
	if review.Review == nil {
		t.Fatalf("rollback review=%+v", review)
	}

	consent := application.RollbackConsent{Installation: application.InstallConsent{Confirmed: true, EditsResolved: true}, Data: application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: review.Review.ReviewID}}
	if _, err := service.ConfirmRollback(started.ID, consent); err != nil {
		t.Fatal(err)
	}

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for exited := false; !exited; {
		select {
		case <-window.exited:
			exited = true
		case <-ticker.C:
			if window.active.Load() == 0 {
				select {
				case <-window.exited:
					exited = true
				default:
					t.Fatalf("rollback ended without installer exit: commits=%d", f.commits)
				}
			}
		case <-t.Context().Done():
			t.Fatal("rollback did not finish")
		}
	}

	if !errors.Is(lifetime.Err(), context.Canceled) || f.commits != 1 {
		t.Fatalf("window=%v commits=%d", lifetime.Err(), f.commits)
	}

	if got := readApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account)); string(got) != `{"opaque":"before upgrade"}` {
		t.Fatalf("live account=%s", got)
	}

	if _, err := os.Stat(f.account + ".mfa"); !os.IsNotExist(err) {
		t.Fatalf("new MFA not removed: %v", err)
	}
}

func waitNativeRollbackState(t *testing.T, service *applicationnative.Service, done func(*applicationnative.InstallationState) bool) *applicationnative.InstallationState {
	t.Helper()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		state, err := service.InstallationStatus()
		if err != nil {
			t.Fatal(err)
		}

		if done(state) {
			return state
		}

		select {
		case <-t.Context().Done():
			t.Fatal("rollback state did not arrive")
		case <-ticker.C:
		}
	}
}
