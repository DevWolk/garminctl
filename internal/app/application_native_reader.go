package app

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

type applicationNativeReader struct {
	*ApplicationReads
	download *application.DownloadReleaseAction
	disabled string
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
