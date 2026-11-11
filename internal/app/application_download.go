package app

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

type applicationPackageDownload struct {
	source                           *updatetransport.GitHub
	key                              string
	stageDirectory, packageDirectory string
}

func (d applicationPackageDownload) Download(ctx context.Context, selected application.Release, target application.DownloadTarget, progress func(application.DownloadProgress)) (result *application.DownloadResult, err error) {
	key, err := applicationStageKey(d.key)
	if err != nil {
		return nil, err
	}

	request := updatetransport.MatchRequest{ReleaseTag: selected.Tag, OS: target.OS, Arch: target.Arch, Installation: updatetransport.InstallKind(target.Installation)}

	publishDownloadProgress(progress, "resolving", 0, 0)

	resolved, err := d.source.Resolve(ctx, request)
	if err != nil {
		return nil, err
	}

	record := applicationReleases([]updatetransport.Release{resolved.Release})[0]
	if err := application.ValidateDownloadRelease(application.DownloadRequest{Tag: selected.Tag, IncludePrerelease: target.IncludePrerelease}, record); err != nil {
		return nil, err
	}

	stageDir, packageDir := d.directories()

	stager, cache, err := newApplicationPackageCache(key, target, stageDir, packageDir)
	if err != nil {
		return nil, err
	}

	if saved, err := existingApplicationPackage(ctx, cache, request, *resolved, progress); saved != nil || err != nil {
		return downloadReceipt(saved), err
	}

	publishDownloadProgress(progress, "downloading", 0, resolved.Package.Size)

	staged, err := stager.Stage(ctx, request, *resolved, func(p updatetransport.StageProgress) {
		publishDownloadProgress(progress, "downloading", p.Written, p.Total)
	})
	if err != nil {
		return nil, err
	}

	defer func() {
		if closeErr := staged.Close(); closeErr != nil {
			result = nil
			err = errors.Join(err, closeErr)
		}
	}()

	publishDownloadProgress(progress, "verifying", resolved.Package.Size, resolved.Package.Size)

	saved, err := cache.Put(ctx, request, staged)
	if err != nil {
		return nil, err
	}

	return downloadReceipt(saved), nil
}

func (d applicationPackageDownload) directories() (string, string) {
	return applicationPackageDirectories(d.stageDirectory, d.packageDirectory)
}

func existingApplicationPackage(ctx context.Context, cache *updatetransport.PackageCache, target updatetransport.MatchRequest, selected updatetransport.ResolvedRelease, progress func(application.DownloadProgress)) (*updatetransport.CachedPackage, error) {
	entries, err := cache.List(ctx)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.Target != target || entry.Release.ReleaseID != selected.ReleaseID || entry.Release.Package != selected.Package || entry.Release.Signature != selected.Signature {
			continue
		}

		publishDownloadProgress(progress, "verifying", selected.Package.Size, selected.Package.Size)

		verified, err := cache.Open(ctx, entry.ID, nil)
		if err != nil {
			return nil, err
		}

		err = verified.Close()
		if err != nil {
			return nil, err
		}

		return &entry, nil
	}

	return nil, nil
}

func downloadReceipt(saved *updatetransport.CachedPackage) *application.DownloadResult {
	if saved == nil {
		return nil
	}

	return &application.DownloadResult{Tag: saved.Release.Release.Tag, CacheID: saved.ID, Digest: saved.Digest, Bytes: saved.Release.Package.Size}
}

func publishDownloadProgress(progress func(application.DownloadProgress), phase string, written, total int64) {
	if progress != nil {
		progress(application.DownloadProgress{Phase: phase, Written: written, Total: total})
	}
}
