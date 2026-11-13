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
// backend probe, watch runtime, account client or release-feed request.
func NewApplicationRecovery(version, compiledKey string) (*application.RecoveryAction, error) {
	cli, err := os.Executable()
	if err != nil {
		return nil, err
	}

	cache, err := NewApplicationPackageCache(cli, version, compiledKey)
	if err != nil {
		return nil, err
	}

	return application.NewRecoveryAction(applicationRecoveryPackages{cache: cache, directory: filepath.Join(paths.Cache(), "application-recovery")}, runtime.GOOS, runtime.GOARCH), nil
}

type applicationRecoveryPackages struct {
	cache     *updatetransport.PackageCache
	directory string
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
	value, err := s.cache.Export(ctx, id, s.directory, nil)
	if err != nil {
		return nil, err
	}

	return &application.RecoveryExport{Record: applicationPackageRecord(value.Entry), Directory: value.Directory, Path: value.Path, SignaturePath: value.SignaturePath}, nil
}
