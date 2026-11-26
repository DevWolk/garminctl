package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

const (
	downloadTestStable = "v4.5.6"
	downloadTestBeta   = "v8.1.0-beta.2"
	downloadTestDEB    = "deb"
)

type downloadInventory struct{ value *application.InventoryResult }

func (f downloadInventory) Info(context.Context) (*application.InventoryResult, error) {
	return f.value, nil
}

type downloadFeed struct {
	calls  int
	values []application.Release
}

func (f *downloadFeed) Releases(context.Context) ([]application.Release, error) {
	f.calls++
	return f.values, nil
}

type downloadPackages struct {
	calls    int
	selected application.Release
	target   application.DownloadTarget
}

func (f *downloadPackages) Download(_ context.Context, selected application.Release, target application.DownloadTarget, progress func(application.DownloadProgress)) (*application.DownloadResult, error) {
	f.calls++
	f.selected = selected
	f.target = target

	if progress != nil {
		progress(application.DownloadProgress{Phase: "downloading", Written: 2, Total: 3})
	}

	return &application.DownloadResult{Tag: selected.Tag, CacheID: "verified-id", Digest: "verified-digest", Bytes: 3}, nil
}

func downloadLocal() *application.InventoryResult {
	return &application.InventoryResult{OS: "linux", Arch: "amd64", Desktop: application.BinaryVersion{Version: version100}, CLI: application.BinaryVersion{Path: "/trusted/garminctl", Version: version100}, Installation: application.InstallationInfo{Kind: downloadTestDEB}}
}

func TestDownloadRechecksSelectionAndUsesOnlyObservedNativeTarget(t *testing.T) {
	t.Parallel()

	feed := &downloadFeed{values: []application.Release{{Tag: downloadTestStable}, {Tag: downloadTestBeta, Prerelease: true}}}
	packages := &downloadPackages{}
	action := application.NewDownloadReleaseAction(downloadInventory{downloadLocal()}, feed, packages)

	result, err := action.Run(t.Context(), application.DownloadRequest{Tag: downloadTestStable}, nil)
	if err != nil || result == nil || result.Tag != downloadTestStable {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	if feed.calls != 1 || packages.calls != 1 || packages.target.CLIPath != "/trusted/garminctl" || packages.target.Installation != downloadTestDEB {
		t.Fatalf("feed=%d packages=%+v", feed.calls, packages)
	}
}

func TestDownloadRejectsIneligibleTagsBeforePackageIO(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, tag string
		records   []application.Release
	}{
		{name: "invalid", tag: "../evil"},
		{name: "missing", tag: "v3.0.0"},
		{name: "draft", tag: downloadTestStable, records: []application.Release{{Tag: downloadTestStable, Draft: true}}},
		{name: "prerelease flag", tag: downloadTestStable, records: []application.Release{{Tag: downloadTestStable, Prerelease: true}}},
		{name: "prerelease suffix", tag: downloadTestBeta, records: []application.Release{{Tag: downloadTestBeta}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			feed := &downloadFeed{values: tc.records}
			packages := &downloadPackages{}

			a := application.NewDownloadReleaseAction(downloadInventory{downloadLocal()}, feed, packages)
			if _, err := a.Run(t.Context(), application.DownloadRequest{Tag: tc.tag}, nil); err == nil || packages.calls != 0 {
				t.Fatalf("err=%v packages=%d", err, packages.calls)
			}
		})
	}
}

func TestDownloadRejectsUnconfirmedNativeStateBeforeNetwork(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"unknown", "mismatch", "CLI error", "missing path", "platform"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			info := downloadLocal()

			switch name {
			case "unknown":
				info.Installation.Kind = "unknown"
			case "mismatch":
				info.CLI.Version = "v99.0.0"
			case "CLI error":
				info.CLI.Error = "probe failed"
			case "missing path":
				info.CLI.Path = ""
			case "platform":
				info.OS = "unsupported"
			}

			feed := &downloadFeed{}
			packages := &downloadPackages{}

			a := application.NewDownloadReleaseAction(downloadInventory{info}, feed, packages)
			if _, err := a.Run(t.Context(), application.DownloadRequest{Tag: downloadTestStable}, nil); err == nil || feed.calls != 0 || packages.calls != 0 {
				t.Fatalf("error=%v feed=%d packages=%d", err, feed.calls, packages.calls)
			}
		})
	}
}

func TestDownloadCancellationAndExplicitPrerelease(t *testing.T) {
	t.Parallel()

	feed := &downloadFeed{values: []application.Release{{Tag: downloadTestBeta}}}
	packages := &downloadPackages{}
	a := application.NewDownloadReleaseAction(downloadInventory{downloadLocal()}, feed, packages)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := a.Run(ctx, application.DownloadRequest{Tag: downloadTestBeta, IncludePrerelease: true}, nil); !errors.Is(err, context.Canceled) || feed.calls != 0 {
		t.Fatalf("canceled=%v calls=%d", err, feed.calls)
	}

	result, err := a.Run(t.Context(), application.DownloadRequest{Tag: downloadTestBeta, IncludePrerelease: true}, nil)
	if err != nil || result == nil || !strings.Contains(result.Tag, "beta") {
		t.Fatalf("prerelease=%+v %v", result, err)
	}
}

// downloadSeries records every package one Run asks for, in order.
type downloadSeries struct {
	tags       []string
	prerelease []bool
	fail       map[string]error
	cancel     context.CancelFunc
}

func (f *downloadSeries) Download(_ context.Context, selected application.Release, target application.DownloadTarget, progress func(application.DownloadProgress)) (*application.DownloadResult, error) {
	f.tags = append(f.tags, selected.Tag)
	f.prerelease = append(f.prerelease, target.IncludePrerelease)

	if progress != nil {
		progress(application.DownloadProgress{Phase: "downloading", Written: 2, Total: 3})
	}

	if err := f.fail[selected.Tag]; err != nil {
		return nil, err
	}

	if f.cancel != nil {
		f.cancel()
	}

	return &application.DownloadResult{Tag: selected.Tag, CacheID: "verified-id", Digest: "verified-digest", Bytes: 3}, nil
}

func TestDownloadAlsoSavesTheRunningVersionAsTheWayBack(t *testing.T) {
	t.Parallel()

	const runningBeta = "v1.0.0-beta.1"

	for _, tc := range []struct {
		name, running, selected string
		records                 []application.Release
		want                    []string
	}{
		{name: "update", running: version100, selected: downloadTestStable, records: []application.Release{{Tag: downloadTestStable}, {Tag: version100}}, want: []string{version100, downloadTestStable}},
		{name: "older selection", running: downloadTestStable, selected: version100, records: []application.Release{{Tag: downloadTestStable}, {Tag: version100}}, want: []string{downloadTestStable, version100}},
		{name: "running prerelease outside the channel", running: runningBeta, selected: downloadTestStable, records: []application.Release{{Tag: downloadTestStable}, {Tag: runningBeta, Prerelease: true}}, want: []string{runningBeta, downloadTestStable}},
		{name: "selection is the running version", running: version100, selected: version100, records: []application.Release{{Tag: version100}}, want: []string{version100}},
		{name: "running version is not published", running: version100, selected: downloadTestStable, records: []application.Release{{Tag: downloadTestStable}}, want: []string{downloadTestStable}},
		{name: "running version is a draft", running: version100, selected: downloadTestStable, records: []application.Release{{Tag: downloadTestStable}, {Tag: version100, Draft: true}}, want: []string{downloadTestStable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			info := downloadLocal()
			info.Desktop.Version, info.CLI.Version = tc.running, tc.running
			packages := &downloadSeries{}

			var phases []string

			a := application.NewDownloadReleaseAction(downloadInventory{info}, &downloadFeed{values: tc.records}, packages)

			result, err := a.Run(t.Context(), application.DownloadRequest{Tag: tc.selected}, func(p application.DownloadProgress) { phases = append(phases, p.Phase) })
			if err != nil || result == nil || result.Tag != tc.selected {
				t.Fatalf("result=%+v err=%v", result, err)
			}

			if strings.Join(packages.tags, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("downloads=%q want %q", packages.tags, tc.want)
			}

			// The way back goes first, whatever the channel of the selection,
			// and its progress is told apart from the selected package.
			if len(tc.want) == 2 && (!packages.prerelease[0] || packages.prerelease[1] || strings.Join(phases, " ") != "recovery downloading") {
				t.Fatalf("prerelease=%v phases=%q", packages.prerelease, phases)
			}
		})
	}
}

func TestDownloadReportsAWayBackThatCouldNotBeSaved(t *testing.T) {
	t.Parallel()

	const beta = "v1.0.1-beta.1"

	offline := errors.New("offline")
	published := []application.Release{{Tag: downloadTestStable}, {Tag: version100}}

	for _, tc := range []struct {
		name    string
		records []application.Release
		fail    map[string]error
		want    []string
		saved   bool
	}{
		{name: "way back fails", records: published, fail: map[string]error{version100: offline}, want: []string{version100, downloadTestStable}, saved: true},
		{name: "selection fails", records: published, fail: map[string]error{downloadTestStable: offline}, want: []string{version100, downloadTestStable}},
		{name: "both fail", records: published, fail: map[string]error{version100: offline, downloadTestStable: offline}, want: []string{version100, downloadTestStable}},
		{name: "release list is ambiguous", records: []application.Release{{Tag: downloadTestStable}, {Tag: beta}, {Tag: beta}}, want: []string{downloadTestStable}, saved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			packages := &downloadSeries{fail: tc.fail}
			a := application.NewDownloadReleaseAction(downloadInventory{downloadLocal()}, &downloadFeed{values: tc.records}, packages)

			result, err := a.Run(t.Context(), application.DownloadRequest{Tag: downloadTestStable}, nil)
			if result != nil || err == nil || (tc.fail != nil && !errors.Is(err, offline)) {
				t.Fatalf("result=%+v err=%v", result, err)
			}

			// The selection is attempted whatever happened to the way back.
			if strings.Join(packages.tags, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("downloads=%q want %q", packages.tags, tc.want)
			}

			// Only a saved selection is reported as saved, with both versions named.
			named := strings.Contains(err.Error(), "release "+downloadTestStable+" is saved") && strings.Contains(err.Error(), version100)
			if named != tc.saved {
				t.Fatalf("saved=%v err=%v", tc.saved, err)
			}
		})
	}
}

func TestDownloadCanceledWhileSavingTheWayBackSkipsTheSelection(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	packages := &downloadSeries{cancel: cancel}
	a := application.NewDownloadReleaseAction(downloadInventory{downloadLocal()}, &downloadFeed{values: []application.Release{{Tag: downloadTestStable}, {Tag: version100}}}, packages)

	result, err := a.Run(ctx, application.DownloadRequest{Tag: downloadTestStable}, nil)
	if result != nil || !errors.Is(err, context.Canceled) || strings.Join(packages.tags, " ") != version100 {
		t.Fatalf("result=%+v err=%v downloads=%q", result, err, packages.tags)
	}
}
