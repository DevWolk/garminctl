package application

import (
	"context"
	"errors"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	architectureAMD64 = "amd64"
	installationSetup = "setup"
	installationRPM   = "rpm"
)

// RecoveryExport identifies an independent authenticated copy for manual recovery.
// It grants no permission to replace binaries or restore user data.
type RecoveryExport struct {
	Record                         PackageRecord
	Directory, Path, SignaturePath string
}

// RecoveryPackages lends metadata and publishes verified copies without a GUI.
type RecoveryPackages interface {
	List(context.Context) ([]PackageRecord, error)
	Export(context.Context, string) (*RecoveryExport, error)
}

// RecoveryAction selects retained packages for the executing CLI's platform.
type RecoveryAction struct {
	packages RecoveryPackages
	os, arch string
}

// NewRecoveryAction performs no IO and requires no running backend or watch.
func NewRecoveryAction(packages RecoveryPackages, os, arch string) *RecoveryAction {
	return &RecoveryAction{packages: packages, os: os, arch: arch}
}

// List returns unverified metadata; only Export authenticates package bytes.
func (a *RecoveryAction) List(ctx context.Context) ([]PackageRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if a.packages == nil || a.os == "" || a.arch == "" {
		return nil, errors.New("application recovery is not configured")
	}

	rows, err := a.packages.List(ctx)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return nil, err
	}

	if len(rows) > 1000 {
		return nil, errors.New("recovery catalog exceeds the record limit")
	}

	result := make([]PackageRecord, 0, len(rows))

	ids := make(map[string]bool, len(rows))
	for _, row := range rows {
		if ids[row.ID] {
			return nil, errors.New("recovery package identity is ambiguous")
		}

		ids[row.ID] = true
		if row.OS != a.os || row.Arch != a.arch || row.Draft {
			continue
		}

		if err := validatePreflightPackage(row, DownloadTarget{OS: a.os, Arch: a.arch, Installation: row.Installation}, true); err != nil {
			return nil, err
		}

		if !recoveryInstallation(row) {
			return nil, errors.New("retained recovery package has an unsupported installation type")
		}

		result = append(result, row)
	}

	slices.SortFunc(result, func(a, b PackageRecord) int {
		if order := semver.Compare(semanticVersion(b.Tag), semanticVersion(a.Tag)); order != 0 {
			return order
		}

		return strings.Compare(a.ID, b.ID)
	})

	return result, nil
}

// Export authenticates one explicit choice without asserting data compatibility.
func (a *RecoveryAction) Export(ctx context.Context, id string) (*RecoveryExport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !validPackageHash(id) {
		return nil, errors.New("select a retained recovery package ID")
	}

	rows, err := a.List(ctx)
	if err != nil {
		return nil, err
	}

	index := slices.IndexFunc(rows, func(row PackageRecord) bool { return row.ID == id })
	if index < 0 {
		return nil, errors.New("the selected recovery package is not retained for this platform")
	}

	result, err := a.packages.Export(ctx, id)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return nil, err
	}

	if result == nil || result.Record != rows[index] || result.Directory == "" || result.Path == "" || result.SignaturePath == "" {
		return nil, errors.New("recovery export differs from the selected package")
	}

	value := *result

	return &value, nil
}

func recoveryInstallation(row PackageRecord) bool {
	if row.Installation == installationPortable {
		return (row.OS == "linux" || row.OS == "windows") && (row.Arch == architectureAMD64 || row.Arch == "arm64")
	}

	return row.Arch == architectureAMD64 && (row.OS == "windows" && row.Installation == installationSetup || row.OS == "linux" && (row.Installation == "deb" || row.Installation == installationRPM))
}
