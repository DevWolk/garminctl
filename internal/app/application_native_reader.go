package app

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

type applicationNativeReader struct {
	*ApplicationReads
	download    *application.DownloadReleaseAction
	disabled    string
	maintenance func(context.Context) (func(), error)
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
	} else {
		result.disabled = "This build has no trusted update verification key. Use the Releases link for manual downloads."
	}

	return result, nil
}

func (r *applicationNativeReader) DownloadDisabledReason() string { return r.disabled }
func (r *applicationNativeReader) Download(ctx context.Context, request application.DownloadRequest, progress func(application.DownloadProgress)) (*application.DownloadResult, error) {
	if r.download == nil {
		return nil, errors.New(r.disabled)
	}

	return r.download.Run(ctx, request, progress)
}
