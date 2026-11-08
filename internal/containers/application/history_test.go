package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

const (
	historyStableTag = "v1.3.0"
	historyBetaTag   = "v2.1.0-beta.1"
	historyDraftTag  = "v8.0.0"
)

type historyFeed struct {
	items  []application.Release
	err    error
	cancel context.CancelFunc
}

func (f historyFeed) Releases(context.Context) ([]application.Release, error) {
	if f.cancel != nil {
		f.cancel()
	}

	return f.items, f.err
}

type historyStore struct {
	snapshot         *application.ReleaseSnapshot
	loadErr, saveErr error
	loads, saves     int
}

func (s *historyStore) Load(context.Context) (*application.ReleaseSnapshot, error) {
	s.loads++
	return s.snapshot, s.loadErr
}

func (s *historyStore) Save(_ context.Context, snapshot application.ReleaseSnapshot) error {
	s.saves++
	s.snapshot = &snapshot

	return s.saveErr
}

func TestReleaseHistoryFreshThenOfflineRechecksChannel(t *testing.T) {
	t.Parallel()

	items := []application.Release{{Tag: historyStableTag, Notes: "plain <text>"}, {Tag: historyBetaTag}, {Tag: historyDraftTag, Draft: true}}
	store := &historyStore{}
	request := application.CheckRequest{CurrentVersion: version100}

	fresh, err := application.NewCheckReleaseHistoryAction(historyFeed{items: items}, store).Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	assertFreshReleaseHistory(t, fresh)

	if store.saves != 1 || store.loads != 0 || !slices.Equal(store.snapshot.Releases, items) || !store.snapshot.CheckedAt.Equal(fresh.CheckedAt) {
		t.Fatalf("store=%+v", store)
	}

	offline := errors.New("offline")
	request.IncludePrerelease = true

	cached, err := application.NewCheckReleaseHistoryAction(historyFeed{err: offline}, store).Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	if !cached.FromCache || cached.RefreshError != offline.Error() || !cached.CheckedAt.Equal(fresh.CheckedAt) || cached.Available.Tag != historyBetaTag || len(cached.History) != 2 || store.saves != 1 {
		t.Fatalf("cached=%+v store=%+v", cached, store)
	}
}

func TestReleaseHistoryFailuresAreExplicit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		feed    historyFeed
		store   historyStore
		wantErr bool
		warning bool
	}{
		{name: "missing cache", feed: historyFeed{err: errors.New("HTTP 429")}, store: historyStore{loadErr: errors.New("missing")}, wantErr: true},
		{name: "corrupt cache", feed: historyFeed{err: errors.New("offline")}, store: historyStore{loadErr: errors.New("bad JSON")}, wantErr: true},
		{name: "missing timestamp", feed: historyFeed{err: errors.New("offline")}, store: historyStore{snapshot: &application.ReleaseSnapshot{Releases: []application.Release{}}}, wantErr: true},
		{name: "duplicate fresh", feed: historyFeed{items: []application.Release{{Tag: historyStableTag}, {Tag: historyStableTag}}}, wantErr: true},
		{name: "duplicate cached", feed: historyFeed{err: errors.New("offline")}, store: historyStore{snapshot: &application.ReleaseSnapshot{CheckedAt: time.Now(), Releases: []application.Release{{Tag: historyStableTag}, {Tag: historyStableTag}}}}, wantErr: true},
		{name: "feed cancellation", feed: historyFeed{err: context.Canceled}, wantErr: true},
		{name: "save failure", feed: historyFeed{items: []application.Release{{Tag: historyStableTag}}}, store: historyStore{saveErr: errors.New("disk full")}, warning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := application.NewCheckReleaseHistoryAction(tc.feed, &tc.store).Run(t.Context(), application.CheckRequest{CurrentVersion: version100})
			if tc.wantErr {
				if err == nil || result != nil {
					t.Fatalf("result=%+v error=%v", result, err)
				}

				if tc.store.saves != 0 {
					t.Fatal("failed check saved history")
				}

				return
			}

			if err != nil || result == nil || result.FromCache || (result.CacheError != "") != tc.warning {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestReleaseHistoryInvalidVersionDoesNotReadOrWriteCache(t *testing.T) {
	t.Parallel()

	store := &historyStore{}

	result, err := application.NewCheckReleaseHistoryAction(historyFeed{err: errors.New("offline")}, store).Run(t.Context(), application.CheckRequest{CurrentVersion: "invalid"})
	if result != nil || err == nil || store.loads != 0 || store.saves != 0 {
		t.Fatalf("result=%+v error=%v store=%+v", result, err, store)
	}
}

func TestReleaseHistoryCallerCancellationDoesNotUseCache(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	store := &historyStore{snapshot: &application.ReleaseSnapshot{CheckedAt: time.Now(), Releases: []application.Release{{Tag: historyStableTag}}}}

	result, err := application.NewCheckReleaseHistoryAction(historyFeed{err: errors.New("network stopped"), cancel: cancel}, store).Run(ctx, application.CheckRequest{CurrentVersion: version100})
	if result != nil || !errors.Is(err, context.Canceled) || store.loads != 0 || store.saves != 0 {
		t.Fatalf("result=%+v error=%v store=%+v", result, err, store)
	}
}

func assertFreshReleaseHistory(t *testing.T, fresh *application.ReleaseHistoryResult) {
	t.Helper()

	if fresh.FromCache || fresh.RefreshError != "" || fresh.CacheError != "" || fresh.CheckedAt.IsZero() || fresh.Available == nil || fresh.Available.Tag != historyStableTag {
		t.Fatalf("fresh=%+v", fresh)
	}
}

func TestReleaseHistoryNilFeedRetainsEmptyOfflineHistory(t *testing.T) {
	t.Parallel()

	store := &historyStore{}
	request := application.CheckRequest{CurrentVersion: version100}

	if _, err := application.NewCheckReleaseHistoryAction(historyFeed{}, store).Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	result, err := application.NewCheckReleaseHistoryAction(historyFeed{err: errors.New("offline")}, store).Run(t.Context(), request)
	if err != nil || result == nil || !result.FromCache || result.History == nil || len(result.History) != 0 {
		t.Fatalf("empty offline history=%+v error=%v", result, err)
	}
}
