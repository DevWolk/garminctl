package application

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"golang.org/x/mod/semver"
)

const installationPortable = "portable"

func validPackageHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func preflightTarget(info *InventoryResult) (DownloadTarget, error) {
	target, err := downloadTarget(info)
	if err != nil {
		return DownloadTarget{}, err
	}

	if info.Desktop.Path == "" || !info.Backend.Owned || info.Backend.Error != "" || info.Backend.Version != info.Desktop.Version {
		return DownloadTarget{}, errors.New("installation requires readable matching desktop, CLI and owned backend builds")
	}

	if target.Installation != installationPortable && info.Installation.Package != inventoryPackage {
		return DownloadTarget{}, errors.New("installation package ownership is not confirmed")
	}

	if (target.Installation == "setup" || target.Installation == installationPortable) && info.Installation.Root == "" {
		return DownloadTarget{}, errors.New("the observed installation directory is unavailable")
	}

	return target, nil
}

func selectPreflightPackages(rows []PackageRecord, request PreflightRequest, target DownloadTarget, rollback bool) (PackageRecord, PackageRecord, error) {
	var (
		current, update           PackageRecord
		currentFound, updateFound bool
	)

	if len(rows) > 1000 {
		return current, update, errors.New("package catalog exceeds the record limit")
	}

	for _, row := range rows {
		if row.ID == request.CacheID {
			if updateFound {
				return current, update, errors.New("selected package identity is ambiguous")
			}

			update, updateFound = row, true
		}

		if samePackageTarget(row, target) && semanticVersion(row.Tag) == semanticVersion(target.CLIVersion) {
			if currentFound {
				return current, update, errors.New("current-version recovery package is ambiguous")
			}

			current, currentFound = row, true
		}
	}

	if !updateFound || update.Tag != request.Tag {
		return current, update, errors.New("the selected release is not retained under this package identity")
	}

	if !currentFound {
		return current, update, ErrRecoveryPackageUnavailable
	}

	if err := validatePreflightPackage(current, target, true); err != nil {
		return current, update, err
	}

	if err := validatePreflightPackage(update, target, request.IncludePrerelease); err != nil {
		return current, update, err
	}

	if err := validatePreflightOrder(update.Tag, target.CLIVersion, rollback); err != nil {
		return current, update, err
	}

	return current, update, nil
}

func validatePreflightOrder(selected, installed string, rollback bool) error {
	order := semver.Compare(semanticVersion(selected), semanticVersion(installed))
	if rollback {
		if order >= 0 {
			return errors.New("explicit rollback requires an older retained package")
		}

		return nil
	}

	if order <= 0 {
		return errors.New("an application update must be newer; downgrade requires explicit rollback")
	}

	return nil
}

func samePackageTarget(record PackageRecord, target DownloadTarget) bool {
	return record.OS == target.OS && record.Arch == target.Arch && record.Installation == target.Installation
}

func validatePreflightPackage(record PackageRecord, target DownloadTarget, prerelease bool) error {
	if !samePackageTarget(record, target) || !validPackageHash(record.ID) || !validPackageHash(record.Digest) || record.Bytes <= 0 || record.Bytes > 512<<20 {
		return errors.New("retained package does not match this installation or its bounded metadata")
	}

	return ValidateDownloadRelease(DownloadRequest{Tag: record.Tag, IncludePrerelease: prerelease}, Release{Tag: record.Tag, Prerelease: record.Prerelease, Draft: record.Draft})
}
