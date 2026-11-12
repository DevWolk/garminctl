package application_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

type handoffInstaller struct {
	prepare func(context.Context, application.InstallationInput) (application.PendingInstallation, error)
}

func (p handoffInstaller) Prepare(ctx context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
	return p.prepare(ctx, input)
}

type pendingInstallation struct {
	commit func(context.Context) error
	close  func() error
}

func (p pendingInstallation) Commit(ctx context.Context) error { return p.commit(ctx) }
func (p pendingInstallation) Close() error                     { return p.close() }

type handoffFixture struct {
	inventory *preflightInventory
	packages  *preflightPackages
	prepared  *application.PreparedUpdate
	gate      *lifecycle.Gate
	snapshot  *application.DataSnapshot
	events    []string
}

func newHandoffFixture(t *testing.T, backup bool) *handoffFixture {
	t.Helper()
	return newHandoffTargetFixture(t, backup, downloadTestDEB)
}

func newHandoffTargetFixture(t *testing.T, backup bool, kind string) *handoffFixture {
	t.Helper()

	inventory, packages, request := preflightFixture()

	inventory.value.Installation.Kind, inventory.value.Installation.Root = kind, "/trusted"
	if kind == preflightKindSetup {
		inventory.value.OS = preflightTestWindows
		for i := range packages.rows {
			packages.rows[i].OS = preflightTestWindows
		}
	}

	for i := range packages.rows {
		packages.rows[i].Installation = kind
	}

	f := &handoffFixture{inventory: inventory, packages: packages, gate: new(lifecycle.Gate)}

	var err error

	f.prepared, err = application.NewPreflightAction(inventory, packages, f.gate.TryMaintenance).Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := f.prepared.Close(); err != nil {
			t.Error(err)
		}
	})

	if backup {
		store := dataSnapshotStore{create: func(context.Context, application.PreflightInfo) (*application.DataSnapshot, error) {
			return &application.DataSnapshot{ID: strings.Repeat("e", 64), Directory: "/private/backup", Files: 2, Bytes: 20}, nil
		}}

		f.snapshot, err = application.NewBackupDataAction(store).Run(t.Context(), f.prepared)
		if err != nil {
			t.Fatal(err)
		}
	}

	return f
}

func (f *handoffFixture) action(t *testing.T, prepare func(), stopErr, commitErr, closeErr error) *application.InstallHandoffAction {
	t.Helper()

	installer := handoffInstaller{prepare: func(_ context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
		f.events = append(f.events, "prepare")
		if input.Info != f.prepared.Info() || input.Current.Record() != input.Info.Current || input.Update.Record() != input.Info.Update || input.Snapshot.ID != strings.Repeat("e", 64) {
			t.Fatal("handoff changed the owned selection or snapshot")
		}

		for _, value := range f.packages.copies {
			if value.closes != 0 {
				t.Fatal("verified copies closed before payload preparation")
			}
		}

		if prepare != nil {
			prepare()
		}

		return pendingInstallation{commit: func(context.Context) error {
			f.events = append(f.events, "commit")
			if f.gate.Snapshot().State != lifecycle.Closed {
				t.Fatal("installer committed before owned backend stop")
			}

			return commitErr
		}, close: func() error { f.events = append(f.events, "close payload"); return closeErr }}, nil
	}}
	stop := func(context.Context) error { f.events = append(f.events, "stop"); f.gate.Stop(); return stopErr }

	return application.NewInstallHandoffAction(f.inventory, installer, stop)
}

func TestInstallHandoffOrdersOwnedPayloadStopAndCommit(t *testing.T) {
	t.Parallel()
	f := newHandoffFixture(t, true)
	// Mutating the caller's receipt must not change the preparation's own snapshot.
	f.snapshot.ID = "caller mutation"

	result, err := f.action(t, nil, nil, nil, nil).Run(t.Context(), f.prepared, application.InstallConsent{Confirmed: true, EditsResolved: true})
	if err != nil || result == nil || result.State != application.HandoffAccepted || result.To != f.prepared.Info().Update.Tag || result.From != f.prepared.Info().Current.Tag {
		t.Fatalf("handoff=%+v %v", result, err)
	}

	if !slices.Equal(f.events, []string{"prepare", "stop", "commit", "close payload"}) {
		t.Fatalf("order=%v", f.events)
	}

	assertHandoffCopiesClosed(t, f)

	if again, err := f.action(t, nil, nil, nil, nil).Run(t.Context(), f.prepared, application.InstallConsent{Confirmed: true, EditsResolved: true}); again != nil || err == nil {
		t.Fatalf("consumed preparation reused: %+v %v", again, err)
	}

	if len(f.events) != 4 {
		t.Fatal("second use reached installer")
	}
}

func TestInstallHandoffRefusesBeforeInstallerOrStop(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"no confirmation", "unsaved edits", "no backup", "local inventory drift", "CLI-only package", "changed verified record", "cancelled request"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kind := downloadTestDEB
			if name == "CLI-only package" {
				kind = preflightKindPortable
			}

			f := newHandoffTargetFixture(t, name != "no backup", kind)
			consent := application.InstallConsent{Confirmed: true, EditsResolved: true}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			switch name {
			case "no confirmation":
				consent.Confirmed = false
			case "unsaved edits":
				consent.EditsResolved = false
			case "local inventory drift":
				f.inventory.value.CLI.Path = "changed"
			case "changed verified record":
				f.packages.copies[0].record.Digest = strings.Repeat("f", 64)
			case "cancelled request":
				cancel()
			}

			result, err := f.action(t, nil, nil, nil, nil).Run(ctx, f.prepared, consent)
			if err == nil || len(f.events) != 0 || (result != nil && result.State != application.HandoffUncommitted) {
				t.Fatalf("refused=%+v %v events=%v", result, err, f.events)
			}

			assertPreflightReleased(t, f.gate, f.packages)
		})
	}
}

func TestInstallHandoffFailureStateNeverInvitesAutomaticReplay(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"stop error", "commit error", "cleanup after acceptance", "drift after prepare", "cancel after prepare"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newHandoffFixture(t, true)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			failure := errors.New("fixture failure")

			var (
				stopErr, commitErr, closeErr error
				onPrepare                    func()
			)

			want := application.HandoffUncommitted

			switch name {
			case "stop error":
				stopErr = failure
				want = application.HandoffStopRequested
			case "commit error":
				commitErr = failure
				want = application.HandoffCommitAttempted
			case "cleanup after acceptance":
				closeErr = failure
				want = application.HandoffAccepted
			case "drift after prepare":
				onPrepare = func() { f.inventory.value.Desktop.Path = "changed" }
			case "cancel after prepare":
				onPrepare = cancel
			}

			result, err := f.action(t, onPrepare, stopErr, commitErr, closeErr).Run(ctx, f.prepared, application.InstallConsent{Confirmed: true, EditsResolved: true})
			assertHandoffFailureOutcome(t, f, name, result, err, want, failure)
		})
	}
}

func assertHandoffCopiesClosed(t *testing.T, f *handoffFixture) {
	t.Helper()

	for _, value := range f.packages.copies {
		if value.closes != 1 {
			t.Fatalf("verified copies closed=%d", value.closes)
		}
	}
}

func TestInstallHandoffPrepareErrorsCleanPartialPayloadWithoutStop(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"prepare failure", "partial payload", "missing payload", "nil configuration", "port busy error"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newHandoffFixture(t, true)
			failure := errors.New("cannot retain installer payload")
			closed, stopped, commits := 0, 0, 0
			installer := handoffInstaller{prepare: func(context.Context, application.InstallationInput) (application.PendingInstallation, error) {
				if name == "port busy error" {
					return nil, application.ErrPreparationBusy
				}

				if name == "missing payload" {
					return nil, nil
				}

				if name == "partial payload" {
					return pendingInstallation{commit: func(context.Context) error { commits++; return nil }, close: func() error { closed++; return failure }}, failure
				}

				return nil, failure
			}}

			var port application.InstallerPreparation = installer
			if name == "nil configuration" {
				port = nil
			}

			action := application.NewInstallHandoffAction(f.inventory, port, func(context.Context) error { stopped++; return nil })

			result, err := action.Run(t.Context(), f.prepared, application.InstallConsent{Confirmed: true, EditsResolved: true})
			if err == nil || stopped != 0 || commits != 0 || (result != nil && result.State != application.HandoffUncommitted) {
				t.Fatalf("result=%+v %v stop=%d commit=%d", result, err, stopped, commits)
			}

			if name == "partial payload" && (closed != 1 || !errors.Is(err, failure)) {
				t.Fatalf("partial cleanup=%d %v", closed, err)
			}

			assertPreflightReleased(t, f.gate, f.packages)
		})
	}
}

func TestInstallHandoffCancelAfterStopDoesNotCommitOrReopen(t *testing.T) {
	t.Parallel()
	f := newHandoffFixture(t, true)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	commits, closed := 0, 0
	installer := handoffInstaller{prepare: func(context.Context, application.InstallationInput) (application.PendingInstallation, error) {
		return pendingInstallation{commit: func(context.Context) error { commits++; return nil }, close: func() error { closed++; return nil }}, nil
	}}
	stop := func(context.Context) error { f.gate.Stop(); cancel(); return nil }

	result, err := application.NewInstallHandoffAction(f.inventory, installer, stop).Run(ctx, f.prepared, application.InstallConsent{Confirmed: true, EditsResolved: true})
	if !errors.Is(err, context.Canceled) || result == nil || result.State != application.HandoffStopRequested || commits != 0 || closed != 1 {
		t.Fatalf("cancel=%+v %v commit=%d close=%d", result, err, commits, closed)
	}

	if f.gate.Snapshot().State != lifecycle.Closed {
		t.Fatal("cancellation reopened stopped admission")
	}

	assertHandoffCopiesClosed(t, f)
}

func TestInstallHandoffConcurrentRefusalPreservesFirstOwner(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newHandoffFixture(t, true)
		entered, allow, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		action := f.action(t, func() { close(entered); <-allow }, nil, nil, nil)

		consent := application.InstallConsent{Confirmed: true, EditsResolved: true}
		go func() { _, err := action.Run(t.Context(), f.prepared, consent); done <- err }()

		synctest.Wait()

		select {
		case <-entered:
		default:
			t.Fatal("preparation did not start")
		}

		result, err := action.Run(t.Context(), f.prepared, consent)

		close(allow)

		if firstErr := <-done; firstErr != nil {
			t.Fatalf("first owner failed: %v", firstErr)
		}

		if result != nil || !errors.Is(err, application.ErrPreparationBusy) {
			t.Fatalf("contender=%+v %v", result, err)
		}

		if !slices.Equal(f.events, []string{"prepare", "stop", "commit", "close payload"}) {
			t.Fatalf("duplicate handoff: %v", f.events)
		}

		assertHandoffCopiesClosed(t, f)
	})
}

func assertHandoffFailureOutcome(t *testing.T, f *handoffFixture, name string, result *application.InstallHandoffResult, err error, want application.HandoffState, failure error) {
	t.Helper()

	if err == nil || result == nil || result.State != want {
		t.Fatalf("failure=%+v %v want=%v", result, err, want)
	}

	if want >= application.HandoffStopRequested && !errors.Is(err, failure) {
		t.Fatalf("failure cause lost: %v", err)
	}

	if name == "cancel after prepare" && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel cause lost: %v", err)
	}

	assertHandoffCopiesClosed(t, f)

	if f.events[len(f.events)-1] != "close payload" {
		t.Fatalf("payload leaked: %v", f.events)
	}

	if want == application.HandoffUncommitted && f.gate.Snapshot().State != lifecycle.Running {
		t.Fatal("pre-stop failure stranded backend")
	}

	if want >= application.HandoffStopRequested && f.gate.Snapshot().State != lifecycle.Closed {
		t.Fatal("failure reopened a stopped backend")
	}
}

func TestInstallHandoffSupportsOnlyNativePackageTargets(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{downloadTestDEB, "rpm", preflightKindSetup} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newHandoffTargetFixture(t, true, kind)

			result, err := f.action(t, nil, nil, nil, nil).Run(t.Context(), f.prepared, application.InstallConsent{Confirmed: true, EditsResolved: true})
			if err != nil || result == nil || result.State != application.HandoffAccepted {
				t.Fatalf("target=%s result=%+v %v", kind, result, err)
			}
		})
	}
}
