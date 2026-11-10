package application

import (
	"context"
	"errors"
	"fmt"
)

// DownloadRequest selects metadata only; callers cannot choose native paths or keys.
type DownloadRequest struct {
	Tag               string `json:"tag"`
	IncludePrerelease bool   `json:"includePrerelease"`
}

// DownloadTarget is re-observed locally for each download, never supplied by JS.
type DownloadTarget struct {
	OS, Arch, Installation, CLIPath, CLIVersion string
	IncludePrerelease                           bool
}

// DownloadProgress describes preparation, counted transfer and verification.
type DownloadProgress struct {
	Phase   string `json:"phase"`
	Written int64  `json:"written"`
	Total   int64  `json:"total"`
}

// DownloadResult identifies a retained verified package, without install authority.
type DownloadResult struct {
	Tag     string `json:"tag"`
	CacheID string `json:"cacheId"`
	Digest  string `json:"digest"`
	Bytes   int64  `json:"bytes"`
}

// InventoryReader supplies current native observations without a watch lease.
type InventoryReader interface {
	Info(context.Context) (*InventoryResult, error)
}

// PackageDownloader resolves the exact selection and retains verified bytes.
type PackageDownloader interface {
	Download(context.Context, Release, DownloadTarget, func(DownloadProgress)) (*DownloadResult, error)
}

// DownloadReleaseAction rechecks native and channel policy before package IO.
type DownloadReleaseAction struct {
	inventory InventoryReader
	feed      ReleaseFeed
	packages  PackageDownloader
}

// NewDownloadReleaseAction borrows current observations, release metadata and package IO.
func NewDownloadReleaseAction(inventory InventoryReader, feed ReleaseFeed, packages PackageDownloader) *DownloadReleaseAction {
	return &DownloadReleaseAction{inventory: inventory, feed: feed, packages: packages}
}

// Run re-observes installation and channel eligibility before retaining a package.
func (a *DownloadReleaseAction) Run(ctx context.Context, request DownloadRequest, progress func(DownloadProgress)) (*DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !validDownloadTag(request.Tag) {
		return nil, errors.New("select a valid application release")
	}

	if a.inventory == nil || a.feed == nil || a.packages == nil {
		return nil, errors.New("application download is not configured")
	}

	info, err := a.inventory.Info(ctx)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	target, err := downloadTarget(info)
	if err != nil {
		return nil, err
	}

	releases, err := a.feed.Releases(ctx)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	selected, err := selectedDownload(request, releases)
	if err != nil {
		return nil, err
	}

	target.IncludePrerelease = request.IncludePrerelease

	result, err := a.packages.Download(ctx, *selected, target, progress)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}

	if err != nil {
		return nil, err
	}

	if err := validateDownloadReceipt(request.Tag, result); err != nil {
		return nil, err
	}

	return result, nil
}

func downloadTarget(info *InventoryResult) (DownloadTarget, error) {
	if info == nil {
		return DownloadTarget{}, errors.New("local installation information is unavailable")
	}

	if !validDownloadBuilds(info) {
		return DownloadTarget{}, errors.New("desktop and CLI builds must match and be readable before downloading")
	}

	if (info.OS != inventoryPlatformLinux && info.OS != inventoryPlatformWindows) || (info.Arch != "amd64" && info.Arch != "arm64") {
		return DownloadTarget{}, errors.New("unsupported native package platform")
	}

	kind := info.Installation.Kind
	if !supportedDownloadInstallation(info.OS, kind) {
		return DownloadTarget{}, errors.New("installation type is not confirmed for both binaries")
	}

	return DownloadTarget{OS: info.OS, Arch: info.Arch, Installation: kind, CLIPath: info.CLI.Path, CLIVersion: info.CLI.Version}, nil
}

func selectedDownload(request DownloadRequest, releases []Release) (*Release, error) {
	history, err := channelHistory(releases, request.IncludePrerelease)
	if err != nil {
		return nil, err
	}

	for _, release := range history {
		if release.Tag == request.Tag {
			return &release, nil
		}
	}

	return nil, fmt.Errorf("release %q is not available in the selected channel", request.Tag)
}

// ValidateDownloadRelease reapplies policy to metadata fetched by exact tag.
// A package resolver cannot silently change the channel or include a draft.
func ValidateDownloadRelease(request DownloadRequest, release Release) error {
	if !validDownloadTag(request.Tag) || release.Tag != request.Tag {
		return errors.New("resolved release differs from the selected tag")
	}

	_, err := selectedDownload(request, []Release{release})

	return err
}

func validDownloadBuilds(info *InventoryResult) bool {
	return info.Desktop.Error == "" && info.CLI.Error == "" && info.CLI.Path != "" && semanticVersion(info.Desktop.Version) != "" && info.CLI.Version == info.Desktop.Version
}

func supportedDownloadInstallation(platform, kind string) bool {
	switch kind {
	case "portable":
		return true
	case "setup":
		return platform == inventoryPlatformWindows
	case "deb", "rpm":
		return platform == inventoryPlatformLinux
	default:
		return false
	}
}

func validateDownloadReceipt(tag string, result *DownloadResult) error {
	if result == nil || result.Tag != tag || result.CacheID == "" || result.Digest == "" || result.Bytes <= 0 {
		return errors.New("download returned an incomplete verified package receipt")
	}

	return nil
}

func validDownloadTag(tag string) bool { return len(tag) <= 256 && semanticVersion(tag) != "" }
