package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/paths"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// NewApplicationRecovery uses this CLI's compiled trust key without a native host,
// backend probe, watch runtime, account client or release-feed request. A build
// without a usable key still lists and removes retained metadata; only export
// reports the missing verifier.
func NewApplicationRecovery(version, compiledKey string) (*application.RecoveryAction, error) {
	cli, err := os.Executable()
	if err != nil {
		return nil, err
	}

	packages := applicationRecoveryPackages{directory: filepath.Join(paths.Cache(), "application-recovery")}

	packages.cache, packages.unverified = NewApplicationPackageCache(cli, version, compiledKey)
	if packages.unverified != nil {
		packages.cache, err = updatetransport.NewPackageCatalog(filepath.Join(paths.Cache(), "application-packages"))
		if err != nil {
			return nil, err
		}
	}

	return application.NewRecoveryAction(packages, runtime.GOOS, runtime.GOARCH), nil
}

type applicationRecoveryPackages struct {
	cache      *updatetransport.PackageCache
	directory  string
	unverified error
}

func (s applicationRecoveryPackages) List(ctx context.Context) ([]application.PackageRecord, error) {
	rows, err := s.cache.List(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]application.PackageRecord, 0, len(rows))
	for _, row := range rows {
		result = append(result, applicationPackageRecord(row))
	}

	return result, nil
}

func (s applicationRecoveryPackages) Export(ctx context.Context, id string) (*application.RecoveryExport, error) {
	if s.unverified != nil {
		return nil, s.unverified
	}

	value, err := s.cache.Export(ctx, id, s.directory, nil)
	if err != nil {
		return nil, err
	}

	return &application.RecoveryExport{Record: applicationPackageRecord(value.Entry), Directory: value.Directory, Path: value.Path, SignaturePath: value.SignaturePath}, nil
}

func (s applicationRecoveryPackages) Unusable(ctx context.Context) ([]application.UnusableEntry, error) {
	_, skipped, err := s.cache.Catalog(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]application.UnusableEntry, 0, len(skipped))
	for _, entry := range skipped {
		result = append(result, application.UnusableEntry{ID: entry.ID, Reason: entry.Reason})
	}

	return result, nil
}

func (s applicationRecoveryPackages) Remove(ctx context.Context, id string) error {
	return s.cache.Remove(ctx, id)
}
