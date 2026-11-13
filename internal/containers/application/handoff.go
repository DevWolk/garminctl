package application

import (
	"context"
	"errors"
)

// InstallConsent is supplied only after the native caller resolves user edits.
type InstallConsent struct {
	Confirmed     bool `json:"confirmed"`
	EditsResolved bool `json:"editsResolved"`
}

// InstallationInput lends verified copies during Prepare; paths never come from a request.
type InstallationInput struct {
	Info            PreflightInfo
	Snapshot        DataSnapshot
	Current, Update VerifiedPackage
}

// PendingInstallation owns its payload independently of PreparedUpdate.
// Before Commit, Close aborts preparation. Once Commit is attempted, Close only
// releases local resources; it cannot promise cancellation or undo installation.
type PendingInstallation interface {
	Commit(context.Context) error
	Close() error
}

// InstallerPreparation validates and retains its own payload without installing.
// Commit must wait for executable owners to exit before OS replacement.
type InstallerPreparation interface {
	Prepare(context.Context, InstallationInput) (PendingInstallation, error)
}

// HandoffState records the furthest attempted action, including uncertain commit.
type HandoffState uint8

// Acceptance acknowledges transfer; it never asserts installation or restart success.
const (
	HandoffUncommitted HandoffState = iota
	HandoffStopRequested
	HandoffCommitAttempted
	HandoffAccepted
)

// InstallHandoffResult remains useful on an error after stop or commit was attempted.
type InstallHandoffResult struct {
	From, To, SnapshotID string
	State                HandoffState
}

// InstallHandoffAction consumes a prepared update through caller-owned platform ports.
type InstallHandoffAction struct {
	inventory InventoryReader
	installer InstallerPreparation
	stopOwned func(context.Context) error
}

// NewInstallHandoffAction creates no owner, process, file or public endpoint.
func NewInstallHandoffAction(inventory InventoryReader, installer InstallerPreparation, stopOwned func(context.Context) error) *InstallHandoffAction {
	return &InstallHandoffAction{inventory: inventory, installer: installer, stopOwned: stopOwned}
}

// Run returns maintenance on pre-stop failure; Stop is irreversible and Commit is never retried.
func (a *InstallHandoffAction) Run(ctx context.Context, prepared *PreparedUpdate, consent InstallConsent) (*InstallHandoffResult, error) {
	if prepared == nil {
		return nil, errors.New("installer handoff requires an owned preparation")
	}

	var result *InstallHandoffResult

	entered := false

	err := prepared.withInfo(ctx, func(bound context.Context, info PreflightInfo) error {
		entered = true

		if err := a.validate(prepared, info, consent); err != nil {
			return err
		}

		if err := a.recheck(bound, info.Inventory); err != nil {
			return err
		}

		result = &InstallHandoffResult{From: info.Current.Tag, To: info.Update.Tag, SnapshotID: prepared.snapshot.ID}
		input := InstallationInput{Info: info, Snapshot: *prepared.snapshot, Current: prepared.current, Update: prepared.update}

		return a.transfer(bound, input, result)
	})
	if entered || !errors.Is(err, ErrPreparationBusy) {
		err = errors.Join(err, prepared.Close())
	}

	return result, err
}

func (a *InstallHandoffAction) validate(prepared *PreparedUpdate, info PreflightInfo, consent InstallConsent) error {
	if !consent.Confirmed || !consent.EditsResolved {
		return errors.New("confirm installation and resolve unsaved edits before handoff")
	}

	if a.inventory == nil || a.installer == nil || a.stopOwned == nil {
		return errors.New("installer handoff is not configured")
	}

	if prepared.snapshot == nil || prepared.snapshot.Directory == "" {
		return errors.New("installer handoff requires this preparation's completed data snapshot")
	}

	if prepared.current == nil || prepared.update == nil || prepared.current.Record() != info.Current || prepared.update.Record() != info.Update {
		return errors.New("verified package identity changed before handoff")
	}

	return validateHandoffTarget(info.Inventory)
}

func validateHandoffTarget(info InventoryResult) error {
	if _, err := preflightTarget(&info); err != nil {
		return err
	}

	if info.Arch != "amd64" {
		return errors.New("native installer handoff requires a supported amd64 package")
	}

	if info.Installation.Kind == installationPortable {
		return errors.New("this installation requires its separate manual or portable update flow")
	}

	return nil
}

func (a *InstallHandoffAction) recheck(ctx context.Context, expected InventoryResult) error {
	value, err := a.inventory.Info(ctx)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return err
	}

	if value == nil || *value != expected {
		return errors.New("the installation changed before installer handoff")
	}

	return nil
}

func (a *InstallHandoffAction) transfer(ctx context.Context, input InstallationInput, result *InstallHandoffResult) (err error) {
	pending, err := a.installer.Prepare(ctx, input)
	if pending != nil {
		defer func() { err = errors.Join(err, pending.Close()) }()
	}

	if err != nil {
		return err
	}

	if pending == nil {
		return errors.New("installer preparation returned no owned payload")
	}

	if err := a.recheck(ctx, input.Info.Inventory); err != nil {
		return err
	}

	result.State = HandoffStopRequested

	if err := a.stopOwned(ctx); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	result.State = HandoffCommitAttempted

	if err := pending.Commit(ctx); err != nil {
		return err
	}

	result.State = HandoffAccepted

	return nil
}
