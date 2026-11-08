package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// ReleaseFeed supplies release metadata on request. The caller owns transport,
// caching and credentials; the Action opens no watch or background job.
type ReleaseFeed interface {
	Releases(context.Context) ([]Release, error)
}

// CheckReleasesAction selects candidates without keeping mutable release state.
type CheckReleasesAction struct{ feed ReleaseFeed }

// NewCheckReleasesAction uses the caller's feed. No default network client exists.
func NewCheckReleasesAction(feed ReleaseFeed) *CheckReleasesAction {
	return &CheckReleasesAction{feed: feed}
}

// Run checks one feed snapshot; transport errors never become successful checks.
func (a *CheckReleasesAction) Run(ctx context.Context, request CheckRequest) (*CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	current := semanticVersion(request.CurrentVersion)
	if current == "" {
		return nil, fmt.Errorf("invalid current application version %q", request.CurrentVersion)
	}

	if a.feed == nil {
		return nil, errors.New("application release feed is not configured")
	}

	releases, err := a.feed.Releases(ctx)
	if err != nil {
		return nil, fmt.Errorf("check application releases: %w", err)
	}

	return selectReleaseSnapshot(ctx, request, current, releases)
}

func selectReleaseSnapshot(ctx context.Context, request CheckRequest, current string, releases []Release) (*CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	history, err := channelHistory(releases, request.IncludePrerelease)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &CheckResult{CurrentVersion: request.CurrentVersion, History: history}
	if len(history) > 0 && semver.Compare(semanticVersion(history[0].Tag), current) > 0 {
		candidate := history[0]
		result.Available = &candidate
	}

	return result, nil
}

func channelHistory(releases []Release, includePrerelease bool) ([]Release, error) {
	history := make([]Release, 0, len(releases))
	seen := make(map[string]bool, len(releases))

	for _, release := range releases {
		version := semanticVersion(release.Tag)
		if release.Draft || version == "" {
			continue
		}

		release.Prerelease = release.Prerelease || semver.Prerelease(version) != ""
		if release.Prerelease && !includePrerelease {
			continue
		}

		if seen[release.Tag] {
			return nil, fmt.Errorf("duplicate application release tag %q", release.Tag)
		}

		seen[release.Tag] = true
		history = append(history, release)
	}

	slices.SortFunc(history, compareReleases)

	return history, nil
}

func compareReleases(left, right Release) int {
	if order := semver.Compare(semanticVersion(left.Tag), semanticVersion(right.Tag)); order != 0 {
		return -order
	}

	// Equal precedence, including different build metadata, needs a repeatable
	// display order. This tie-breaker must never decide whether to upgrade.
	return strings.Compare(left.Tag, right.Tag)
}

// semanticVersion accepts full SemVer with an optional leading v. Go module
// shorthands v1/v1.2 are deliberately excluded from application release tags.
func semanticVersion(tag string) string {
	version := "v" + strings.TrimPrefix(tag, "v")
	core, _, _ := strings.Cut(version, "-")

	core, _, _ = strings.Cut(core, "+")
	if strings.Count(core, ".") != 2 || !semver.IsValid(version) {
		return ""
	}

	return version
}
