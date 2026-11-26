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

// phaseRecovery reports the transfer of the running version's package, apart
// from the phases of the selected one.
const phaseRecovery = "recovery"

// DownloadProgress describes preparation, counted transfer and verification.
// The package of the running version is reported under the recovery phase.
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
// It also retains the running version's published package: see saveWayBack.
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

	target, releases, err := a.observe(ctx)
	if err != nil {
		return nil, err
	}

	selected, err := selectedDownload(request, releases)
	if err != nil {
		return nil, err
	}

	target.IncludePrerelease = request.IncludePrerelease

	// The way back goes first. Retention after a save always spares the
	// running version, but not a selection that was saved earlier and is only
	// verified again: saved last, the way back could push that selection out.
	wayBack := a.saveWayBack(ctx, releases, target, selected.Tag, progress)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result, err := a.save(ctx, *selected, target, progress)
	if err != nil {
		return nil, err
	}

	if wayBack != nil {
		return nil, fmt.Errorf("release %s is saved, but %w", selected.Tag, wayBack)
	}

	return result, nil
}

// observe reads the installation and the release list for one download.
func (a *DownloadReleaseAction) observe(ctx context.Context) (DownloadTarget, []Release, error) {
	info, err := a.inventory.Info(ctx)
	if err != nil {
		return DownloadTarget{}, nil, err
	}

	if err := ctx.Err(); err != nil {
		return DownloadTarget{}, nil, err
	}

	target, err := downloadTarget(info)
	if err != nil {
		return DownloadTarget{}, nil, err
	}

	releases, err := a.feed.Releases(ctx)
	if err != nil {
		return DownloadTarget{}, nil, err
	}

	return target, releases, ctx.Err()
}

// save retains one release and checks its receipt. A canceled or expired
// context is returned as it is, whatever the downloader reported.
func (a *DownloadReleaseAction) save(ctx context.Context, release Release, target DownloadTarget, progress func(DownloadProgress)) (*DownloadResult, error) {
	result, err := a.packages.Download(ctx, release, target, progress)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}

	if err != nil {
		return nil, err
	}

	if err := validateDownloadReceipt(release.Tag, result); err != nil {
		return nil, err
	}

	return result, nil
}

// saveWayBack retains the published package of the running version beside a
// different selection. Preflight verifies that package before an installation
// or a rollback, so a download without it would prepare a package that cannot
// be installed. A running build without a published release has no such
// package; preflight refuses it later. Its error does not stop the selection
// from being saved: Run reports it afterwards.
func (a *DownloadReleaseAction) saveWayBack(ctx context.Context, releases []Release, target DownloadTarget, selected string, progress func(DownloadProgress)) error {
	running, found, err := runningRelease(releases, target.CLIVersion)
	if err != nil {
		return wayBackError(target.CLIVersion, err)
	}

	if !found || semanticVersion(running.Tag) == semanticVersion(selected) {
		return nil
	}

	// The running version is kept whatever channel the selection came from.
	target.IncludePrerelease = true

	if _, err := a.save(ctx, running, target, recoveryProgress(progress)); err != nil {
		return wayBackError(running.Tag, err)
	}

	return nil
}

func wayBackError(version string, err error) error {
	return fmt.Errorf("the running version %s could not be saved as the way back: %w", version, err)
}

// runningRelease finds the published release of the installed version. Drafts
// are never offered; a prerelease is, because it is what runs now.
func runningRelease(releases []Release, installed string) (Release, bool, error) {
	version := semanticVersion(installed)
	if version == "" {
		return Release{}, false, nil
	}

	history, err := channelHistory(releases, true)
	if err != nil {
		return Release{}, false, err
	}

	for _, release := range history {
		if semanticVersion(release.Tag) == version {
			return release, true, nil
		}
	}

	return Release{}, false, nil
}

// recoveryProgress reports the transfer of the running version under its own
// phase, so that a window does not present it as the selected package.
func recoveryProgress(progress func(DownloadProgress)) func(DownloadProgress) {
	if progress == nil {
		return nil
	}

	return func(p DownloadProgress) {
		p.Phase = phaseRecovery
		progress(p)
	}
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
