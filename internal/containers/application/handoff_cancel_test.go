package application

import (
	"context"
	"errors"
	"testing"
)

// delayedHandoffContext keeps asynchronous cancellation delivery queued until
// the handoff has returned. Err already reflects cancellation, as it can before
// context.AfterFunc's callback is scheduled on a real goroutine.
type delayedHandoffContext struct{ context.Context } //nolint:containedctx // Test context controls callback scheduling while delegating lifetime cancellation.

func (delayedHandoffContext) Value(any) any { return nil }
func (delayedHandoffContext) AfterFunc(func()) func() bool {
	return func() bool { return true }
}

type cancelHandoffInventory struct{ info InventoryResult }

func (s cancelHandoffInventory) Info(context.Context) (*InventoryResult, error) {
	value := s.info
	return &value, nil
}

type cancelHandoffInstaller struct {
	cancel func()
	commit func() error
}

func (s cancelHandoffInstaller) Prepare(context.Context, InstallationInput) (PendingInstallation, error) {
	s.cancel()
	return cancelHandoffPending{commit: s.commit}, nil
}

type cancelHandoffPending struct{ commit func() error }

func (s cancelHandoffPending) Commit(context.Context) error { return s.commit() }
func (cancelHandoffPending) Close() error                   { return nil }

func TestInstallHandoffChecksOwnedCancellationBeforeStopAndCommit(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"payload", "stop"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			prepared := cancelHandoffPreparation(t)
			t.Cleanup(func() {
				if err := prepared.Close(); err != nil {
					t.Error(err)
				}
			})

			stops, commits := 0, 0
			installer := cancelHandoffInstaller{cancel: func() {
				if phase == "payload" {
					prepared.cancel()
				}
			}, commit: func() error { commits++; return nil }}
			stop := func(context.Context) error { stops++; prepared.cancel(); return nil }
			action := NewInstallHandoffAction(cancelHandoffInventory{prepared.info.Inventory}, installer, stop)
			value, err := action.Run(t.Context(), prepared, InstallConsent{Confirmed: true, EditsResolved: true})

			wantStops, wantState := 0, HandoffUncommitted
			if phase == "stop" {
				wantStops, wantState = 1, HandoffStopRequested
			}

			if !errors.Is(err, context.Canceled) || value == nil || value.State != wantState || stops != wantStops || commits != 0 {
				t.Fatalf("cancel at %s=%+v %v stops=%d commits=%d", phase, value, err, stops, commits)
			}
		})
	}
}

func cancelHandoffPreparation(t *testing.T) *PreparedUpdate {
	t.Helper()
	lifetime, cancel := context.WithCancel(t.Context())
	current, update := recoveryRecord("a", "v1.2.0"), recoveryRecord("b", "v1.3.0")
	info := InventoryResult{OS: "linux", Arch: "amd64", Desktop: BinaryVersion{Path: "/trusted/desktop", Version: current.Tag}, CLI: BinaryVersion{Path: "/trusted/cli", Version: current.Tag}, Backend: BackendInfo{Owned: true, Version: current.Tag}, Installation: InstallationInfo{Kind: "deb", Package: "garminctl"}}

	return &PreparedUpdate{info: PreflightInfo{Inventory: info, Current: current, Update: update}, snapshot: &DataSnapshot{ID: "fixture-snapshot", Directory: "/private/snapshot"}, current: &restorePackage{value: current}, update: &restorePackage{value: update}, lifetime: delayedHandoffContext{lifetime}, cancel: cancel}
}
