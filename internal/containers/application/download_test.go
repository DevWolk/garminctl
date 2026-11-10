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
