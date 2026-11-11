package application

import (
	"context"
	"errors"
	"sync"
)

// ErrRecoveryPackageUnavailable requires a retained current-version package before installation.
var ErrRecoveryPackageUnavailable = errors.New("the current version has no retained recovery package; use the manual installer")

// PreflightRequest selects an existing cache record without supplying paths or keys.
type PreflightRequest struct {
	Tag               string `json:"tag"`
	CacheID           string `json:"cacheId"`
	IncludePrerelease bool   `json:"includePrerelease"`
}

// PackageRecord is catalog metadata until Open verifies its bytes and signature.
type PackageRecord struct {
	ID, Tag, Digest, OS, Arch, Installation string
	Bytes                                   int64
	Prerelease, Draft                       bool
}

// VerifiedPackage lends an immutable record and owns its temporary verified copy.
type VerifiedPackage interface {
	Record() PackageRecord
	Close() error
}

// PreflightPackages borrows the current CLI/key and never fetches release metadata.
type PreflightPackages interface {
	List(context.Context, DownloadTarget) ([]PackageRecord, error)
	Open(context.Context, DownloadTarget, string) (VerifiedPackage, error)
}

// PreflightInfo is a snapshot, not permission to run an installer or restore data.
type PreflightInfo struct {
	Inventory InventoryResult
	Current   PackageRecord
	Update    PackageRecord
}

// PreparedUpdate owns both verified copies and maintenance until Close.
// The caller must close it, including after caller/window cancellation.
type PreparedUpdate struct {
	info            PreflightInfo
	current, update VerifiedPackage
	release         func()
	once            sync.Once
	closeErr        error
}

// Info returns copied metadata; it does not grant installer authority.
func (p *PreparedUpdate) Info() PreflightInfo { return p.info }

// Close joins copy cleanup before returning maintenance; repeated calls share its error.
func (p *PreparedUpdate) Close() error {
	p.once.Do(func() {
		if p.update != nil {
			p.closeErr = errors.Join(p.closeErr, p.update.Close())
		}

		if p.current != nil {
			p.closeErr = errors.Join(p.closeErr, p.current.Close())
		}

		if p.release != nil {
			p.release()
		}
	})

	return p.closeErr
}

// PreflightAction rechecks one owned installation and its retained package pair.
type PreflightAction struct {
	inventory   InventoryReader
	packages    PreflightPackages
	maintenance func(context.Context) (func(), error)
}

// NewPreflightAction creates no IO, runtime or device owner.
func NewPreflightAction(inventory InventoryReader, packages PreflightPackages, maintenance func(context.Context) (func(), error)) *PreflightAction {
	return &PreflightAction{inventory: inventory, packages: packages, maintenance: maintenance}
}

// Run returns caller-owned verified copies; any failure releases all acquired resources.
func (a *PreflightAction) Run(ctx context.Context, request PreflightRequest) (result *PreparedUpdate, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !validDownloadTag(request.Tag) || !validPackageHash(request.CacheID) {
		return nil, errors.New("select an existing application package and release tag")
	}

	if a.inventory == nil || a.packages == nil || a.maintenance == nil {
		return nil, errors.New("application preflight is not configured")
	}

	prepared := new(PreparedUpdate)

	defer func() {
		if result == nil {
			err = errors.Join(err, prepared.Close())
		}
	}()

	prepared.release, err = a.maintenance(ctx)
	if err != nil {
		return nil, err
	}

	if prepared.release == nil {
		return nil, errors.New("maintenance did not return an owned ticket")
	}

	info, target, err := a.readPreflightInventory(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := a.packages.List(ctx, target)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	current, update, err := selectPreflightPackages(rows, request, target)
	if err != nil {
		return nil, err
	}

	prepared.info = PreflightInfo{Inventory: info, Current: current, Update: update}
	if err := a.verifyPrepared(ctx, target, prepared); err != nil {
		return nil, err
	}

	return prepared, nil
}

func (a *PreflightAction) verifyPrepared(ctx context.Context, target DownloadTarget, prepared *PreparedUpdate) error {
	var err error

	prepared.current, err = openPreflightPackage(ctx, a.packages, target, prepared.info.Current)
	if err != nil {
		return err
	}

	prepared.update, err = openPreflightPackage(ctx, a.packages, target, prepared.info.Update)
	if err != nil {
		return err
	}

	again, _, err := a.readPreflightInventory(ctx)
	if err != nil {
		return err
	}

	if again != prepared.info.Inventory {
		return errors.New("the local installation changed during package verification")
	}

	return nil
}

func (a *PreflightAction) readPreflightInventory(ctx context.Context) (InventoryResult, DownloadTarget, error) {
	value, err := a.inventory.Info(ctx)
	if canceled := ctx.Err(); canceled != nil {
		return InventoryResult{}, DownloadTarget{}, canceled
	}

	if err != nil {
		return InventoryResult{}, DownloadTarget{}, err
	}

	target, err := preflightTarget(value)
	if err != nil {
		return InventoryResult{}, DownloadTarget{}, err
	}

	return *value, target, nil
}

func openPreflightPackage(ctx context.Context, packages PreflightPackages, target DownloadTarget, record PackageRecord) (VerifiedPackage, error) {
	verified, err := packages.Open(ctx, target, record.ID)
	if canceled := ctx.Err(); canceled != nil {
		return verified, errors.Join(err, canceled)
	}

	if err != nil {
		return verified, err
	}

	if verified == nil || verified.Record() != record {
		return verified, errors.New("verified package differs from the selected catalog record")
	}

	return verified, nil
}
