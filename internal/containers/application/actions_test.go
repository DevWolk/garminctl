package application_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

const (
	version100       = "v1.0.0"
	version190       = "v1.9.0"
	version1100Build = "v1.10.0+build.2"
)

type fakeFeed struct {
	releases []application.Release
	err      error
	calls    int
	before   func(context.Context)
}

var _ application.ReleaseFeed = (*fakeFeed)(nil)

func (f *fakeFeed) Releases(ctx context.Context) ([]application.Release, error) {
	f.calls++

	if f.before != nil {
		f.before(ctx)
	}

	return f.releases, f.err
}

func TestCheckReleasesActionRunChannels(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		prerelease bool
		want       []string
	}{
		{name: "stable default", want: []string{version1100Build, "v1.9.9", "v1.2.0"}},
		{name: "explicit prerelease", prerelease: true, want: []string{"v2.0.0-beta.1", "v1.99.0", version1100Build, "v1.9.9", "v1.2.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := []application.Release{
				{Tag: "v1.9.9", PublishedAt: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)},
				{Tag: "v9.0.0", Draft: true},
				{Tag: version1100Build, Name: "Latest stable", Notes: "<b>literal release notes</b>"},
				{Tag: "v2.0.0-beta.1"}, // A false feed flag cannot turn a SemVer prerelease into stable.
				{Tag: "v1.99.0", Prerelease: true},
				{Tag: "v1.2.0"},
			}
			before := slices.Clone(input)
			feed := &fakeFeed{releases: input, before: func(ctx context.Context) {
				if ctx != t.Context() {
					t.Error("feed did not receive caller context")
				}
			}}

			result, err := application.NewCheckReleasesAction(feed).Run(t.Context(), application.CheckRequest{
				CurrentVersion: version100, IncludePrerelease: tc.prerelease,
			})
			if err != nil {
				t.Fatal(err)
			}

			if got := releaseTags(result.History); !slices.Equal(got, tc.want) {
				t.Fatalf("history=%v, want %v", got, tc.want)
			}

			if result.Available == nil || result.Available.Tag != tc.want[0] {
				t.Fatalf("available=%+v, want %s", result.Available, tc.want[0])
			}

			if !reflect.DeepEqual(input, before) || feed.calls != 1 {
				t.Fatal("check changed feed data or failed to borrow the caller's context exactly once")
			}

			checkReleaseMetadata(t, result.History)
		})
	}
}

func checkReleaseMetadata(t *testing.T, history []application.Release) {
	t.Helper()

	for _, release := range history {
		switch release.Tag {
		case version1100Build:
			if release.Name != "Latest stable" || release.Notes != "<b>literal release notes</b>" {
				t.Fatal("selection changed display metadata")
			}
		case "v2.0.0-beta.1":
			if !release.Prerelease {
				t.Fatal("SemVer prerelease was presented as stable")
			}
		}
	}
}

func TestCheckReleasesActionRunVersionOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, current string
		tags, want    []string
		available     string
	}{
		{
			name: "SemVer prerelease precedence", current: "v0.9.0",
			tags:      []string{"v1.0.0-beta.2", "v1.0.0-alpha", version100, "v1.0.0-beta.11", "v1.0.0-alpha.1", "v1.0.0-beta", "v1.0.0-rc.1", "v1.0.0-alpha.beta"},
			want:      []string{version100, "v1.0.0-rc.1", "v1.0.0-beta.11", "v1.0.0-beta.2", "v1.0.0-beta", "v1.0.0-alpha.beta", "v1.0.0-alpha.1", "v1.0.0-alpha"},
			available: version100,
		},
		{
			name: "optional v prefix retains exact tags", current: "1.0.0",
			tags: []string{version190, "1.10.0"}, want: []string{"1.10.0", version190}, available: "1.10.0",
		},
		{
			name: "equal metadata is not an upgrade", current: "v1.0.0+installed",
			tags: []string{"v1.0.0+published"}, want: []string{"v1.0.0+published"},
		},
		{
			name: "stable replaces same-version candidate", current: "v1.0.0-rc.1",
			tags: []string{version100}, want: []string{version100}, available: version100,
		},
		{
			name: "newer installed version never downgrades", current: "v2.0.0-rc.1",
			tags: []string{version190}, want: []string{version190},
		},
		{
			name: "metadata ties are deterministic", current: version100,
			tags: []string{"v1.1.0+bbb", "v1.1.0+aaa"}, want: []string{"v1.1.0+aaa", "v1.1.0+bbb"}, available: "v1.1.0+aaa",
		},
		{
			name: "unusable feed tags are excluded", current: version100,
			tags: []string{"nightly", "", "v1", "v1.2", "v01.0.0", "v1.0.0-01", " v9.0.0", "v1.2.3"},
			want: []string{"v1.2.3"}, available: "v1.2.3",
		},
		{name: "empty feed", current: version100, want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, reverse := range []bool{false, true} {
				feed := new(fakeFeed)
				for _, tag := range tc.tags {
					feed.releases = append(feed.releases, application.Release{Tag: tag})
				}

				if reverse {
					slices.Reverse(feed.releases)
				}

				result, err := application.NewCheckReleasesAction(feed).Run(t.Context(), application.CheckRequest{
					CurrentVersion: tc.current, IncludePrerelease: true,
				})
				if err != nil {
					t.Fatal(err)
				}

				if result.CurrentVersion != tc.current || result.History == nil || !slices.Equal(releaseTags(result.History), tc.want) {
					t.Fatalf("result=%+v, want history=%v and current=%s", result, tc.want, tc.current)
				}

				checkAvailable(t, result.Available, tc.available)
			}
		})
	}
}

func checkAvailable(t *testing.T, available *application.Release, tag string) {
	t.Helper()

	if tag == "" {
		if available != nil {
			t.Fatalf("unexpected update: %+v", available)
		}

		return
	}

	if available == nil || available.Tag != tag {
		t.Fatalf("available=%+v, want %s", available, tag)
	}
}

func TestCheckReleasesActionRunRejectsInvalidCurrentBeforeFeed(t *testing.T) {
	t.Parallel()

	for _, current := range []string{"", "dev", "v1", "v1.2", "v01.0.0", "v1.0.0-01", "v1.0.0+", " v1.0.0"} {
		t.Run(current, func(t *testing.T) {
			t.Parallel()

			feed := new(fakeFeed)

			result, err := application.NewCheckReleasesAction(feed).Run(t.Context(), application.CheckRequest{CurrentVersion: current})
			if err == nil || result != nil || feed.calls != 0 {
				t.Fatalf("invalid current version reached feed: result=%+v err=%v calls=%d", result, err, feed.calls)
			}
		})
	}
}

func TestCheckReleasesActionRunPreservesFeedErrors(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"offline", "rate limited"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			failure := errors.New(name)
			feed := &fakeFeed{releases: []application.Release{{Tag: "v9.0.0"}}, err: failure}

			result, err := application.NewCheckReleasesAction(feed).Run(t.Context(), application.CheckRequest{CurrentVersion: version100})
			if result != nil || !errors.Is(err, failure) || feed.calls != 1 {
				t.Fatalf("feed error became success: result=%+v err=%v calls=%d", result, err, feed.calls)
			}
		})
	}
}

func TestCheckReleasesActionRunCancellation(t *testing.T) {
	t.Parallel()

	for _, duringFeed := range []bool{false, true} {
		name := "before feed"
		if duringFeed {
			name = "during feed"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			feed := &fakeFeed{releases: []application.Release{{Tag: "v9.0.0"}}}
			wantCalls := 0

			if duringFeed {
				feed.before = func(context.Context) { cancel() }
				wantCalls = 1
			} else {
				cancel()
			}

			result, err := application.NewCheckReleasesAction(feed).Run(ctx, application.CheckRequest{CurrentVersion: version100})
			if result != nil || !errors.Is(err, context.Canceled) || feed.calls != wantCalls {
				t.Fatalf("cancelled check: result=%+v err=%v calls=%d", result, err, feed.calls)
			}
		})
	}
}

func TestCheckReleasesActionRunOwnsResultData(t *testing.T) {
	t.Parallel()

	const originalName = "Original"

	feed := &fakeFeed{releases: []application.Release{{Tag: "v2.0.0", Name: originalName}}}
	action := application.NewCheckReleasesAction(feed)
	request := application.CheckRequest{CurrentVersion: version100}

	result, err := action.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	result.Available.Name = "changed candidate"
	if result.History[0].Name != originalName {
		t.Fatal("candidate aliases history")
	}

	result.History[0].Name = "changed history"
	if feed.releases[0].Name != originalName {
		t.Fatal("history aliases feed")
	}

	again, err := action.Run(t.Context(), request)
	if err != nil || again == nil || again.Available == nil || again.Available.Name != originalName {
		t.Fatalf("prior caller mutation survived a new check: result=%+v err=%v", again, err)
	}
}

func TestCheckReleasesActionRunRejectsDuplicateTags(t *testing.T) {
	t.Parallel()

	feed := &fakeFeed{releases: []application.Release{{Tag: "v2.0.0", Name: "first"}, {Tag: "v2.0.0", Name: "different"}}}

	result, err := application.NewCheckReleasesAction(feed).Run(t.Context(), application.CheckRequest{CurrentVersion: version100})
	if err == nil || result != nil {
		t.Fatalf("ambiguous release identity accepted: result=%+v err=%v", result, err)
	}
}

func TestCheckReleasesActionRunWithoutFeed(t *testing.T) {
	t.Parallel()

	var action application.CheckReleasesAction

	result, err := action.Run(t.Context(), application.CheckRequest{CurrentVersion: version100})
	if err == nil || result != nil {
		t.Fatalf("unconfigured action accepted: result=%+v err=%v", result, err)
	}
}

func releaseTags(releases []application.Release) []string {
	result := make([]string, len(releases))
	for i, release := range releases {
		result[i] = release.Tag
	}

	return result
}
