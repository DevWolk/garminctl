package app

import (
	"context"
	"errors"
	"slices"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

type applicationNativeReader struct {
	*ApplicationReads
	download        *application.DownloadReleaseAction
	preflight       *application.PreflightAction
	snapshot        *application.BackupDataAction
	snapshotContext func(context.Context) (context.Context, context.CancelFunc, error)
	disabled        string
	maintenance     func(context.Context) (func(), error)
	handoff         *application.InstallHandoffAction
	rollbackFor     func() (*application.RollbackAction, error)
}

func (b *ApplicationBackend) nativeReader(config ApplicationReadConfig, compiledKey string) (*applicationNativeReader, error) {
	reader, err := newApplicationNativeReader(config, compiledKey)
	if err != nil {
		return nil, err
	}

	address := ""
	if config.Inventory.BackendURL != nil {
		address = config.Inventory.BackendURL.String()
	}

	reader.maintenance = b.maintenanceFor(address, config.Inventory.BackendOwned)

	reader.snapshotContext = b.snapshotContextFor(address, config.Inventory.BackendOwned)
	if reader.disabled == "" {
		reader.handoff = application.NewInstallHandoffAction(reader.ApplicationReads, applicationInstallerPort{key: compiledKey, args: slices.Clone(config.RestartArgs)}, b.stopFor(address, config.Inventory.BackendOwned))
		reader.rollbackFor = func() (*application.RollbackAction, error) {
			return b.rollbackAction(reader, address, config.Inventory.BackendOwned)
		}
	}

	return reader, nil
}

// TryMaintenance borrows only the bound owned backend's existing admission gate.
// It is an internal capability, not a method on the native Wails Service.
func (r *applicationNativeReader) TryMaintenance(ctx context.Context) (func(), error) {
	if r.maintenance == nil {
		return nil, errors.New("owned backend control is not configured")
	}

	return r.maintenance(ctx)
}

func newApplicationNativeReader(config ApplicationReadConfig, compiledKey string) (*applicationNativeReader, error) {
	reads, err := NewApplicationReads(config)
	if err != nil {
		return nil, err
	}

	result := &applicationNativeReader{ApplicationReads: reads}

	_, keyErr := applicationStageKey(compiledKey)
	if keyErr == nil {
		source := updatetransport.NewGitHub(config.ReleaseClient)
		result.download = application.NewDownloadReleaseAction(reads, applicationReleaseFeed{source: source}, applicationPackageDownload{source: source, key: compiledKey})
		result.preflight = application.NewPreflightAction(reads, applicationPackageStore{key: compiledKey}, result.TryMaintenance)
		result.snapshot = application.NewBackupDataAction(applicationDataSnapshots{build: config.Inventory.DesktopVersion})
	} else {
		result.disabled = "This build has no trusted update verification key. Use the Releases link for manual downloads."
	}

	return result, nil
}

// Preflight verifies a retained update and current-version recovery package internally.
// It exposes no additional method on the Wails Service.
func (r *applicationNativeReader) Preflight(ctx context.Context, request application.PreflightRequest) (*application.PreparedUpdate, error) {
	if r.preflight == nil {
		return nil, errors.New(r.disabled)
	}

	return r.preflight.Run(ctx, request)
}

func (r *applicationNativeReader) DownloadDisabledReason() string { return r.disabled }
func (r *applicationNativeReader) Download(ctx context.Context, request application.DownloadRequest, progress func(application.DownloadProgress)) (*application.DownloadResult, error) {
	if r.download == nil {
		return nil, errors.New(r.disabled)
	}

	return r.download.Run(ctx, request, progress)
}
