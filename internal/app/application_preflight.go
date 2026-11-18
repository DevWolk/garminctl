package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/paths"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

type applicationPackageStore struct {
	key                              string
	stageDirectory, packageDirectory string
}

func newApplicationPackageCache(key []byte, target application.DownloadTarget, stage, packages string) (*updatetransport.Stager, *updatetransport.PackageCache, error) {
	stager, err := updatetransport.NewStager(updatetransport.StagerConfig{CLIPath: target.CLIPath, BuildVersion: target.CLIVersion, PublicKey: key, CacheDir: stage})
	if err != nil {
		return nil, nil, err
	}

	cache, err := updatetransport.NewPackageCache(packages, stager)

	return stager, cache, err
}

func applicationPackageDirectories(stage, packages string) (string, string) {
	if stage == "" {
		stage = applicationStageCache()
	}

	if packages == "" {
		packages = filepath.Join(paths.Cache(), "application-packages")
	}

	return stage, packages
}

func (s applicationPackageStore) cache(target application.DownloadTarget) (*updatetransport.PackageCache, error) {
	key, err := applicationStageKey(s.key)
	if err != nil {
		return nil, err
	}

	stage, packages := applicationPackageDirectories(s.stageDirectory, s.packageDirectory)
	_, cache, err := newApplicationPackageCache(key, target, stage, packages)

	return cache, err
}

func (s applicationPackageStore) List(ctx context.Context, target application.DownloadTarget) ([]application.PackageRecord, error) {
	cache, err := s.cache(target)
	if err != nil {
		return nil, err
	}

	rows, err := cache.List(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]application.PackageRecord, 0, len(rows))
	for _, row := range rows {
		result = append(result, applicationPackageRecord(row))
	}

	return result, nil
}

func (s applicationPackageStore) Open(ctx context.Context, target application.DownloadTarget, id string) (application.VerifiedPackage, error) {
	cache, err := s.cache(target)
	if err != nil {
		return nil, err
	}

	rows, err := cache.List(ctx)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		if row.ID != id {
			continue
		}

		verified, err := cache.Open(ctx, id, nil)
		if err != nil {
			return nil, err
		}
		// Open validates the same identity, binding its target to the actual digest.
		row.Release, row.Digest = verified.Release(), verified.Digest()

		return &verifiedApplicationPackage{record: applicationPackageRecord(row), staged: verified}, nil
	}

	return nil, errors.Join(os.ErrNotExist, errors.New("selected application package is not retained"))
}

type verifiedApplicationPackage struct {
	record application.PackageRecord
	staged *updatetransport.StagedPackage
}

func (p *verifiedApplicationPackage) Record() application.PackageRecord { return p.record }
func (p *verifiedApplicationPackage) Close() error                      { return p.staged.Close() }

func applicationPackageRecord(value updatetransport.CachedPackage) application.PackageRecord {
	return application.PackageRecord{
		ID: value.ID, Tag: value.Release.Release.Tag, Digest: value.Digest,
		OS: value.Target.OS, Arch: value.Target.Arch, Installation: string(value.Target.Installation),
		Bytes: value.Release.Package.Size, Prerelease: value.Release.Release.Prerelease, Draft: value.Release.Release.Draft,
	}
}
