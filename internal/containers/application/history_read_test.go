package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

func TestSavedHistoryReadsWithoutRefreshOrPersistenceAndRechecksChannel(t *testing.T) {
	t.Parallel()

	checked := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store := &historyStore{snapshot: &application.ReleaseSnapshot{CheckedAt: checked, Releases: []application.Release{{Tag: historyStableTag}, {Tag: historyBetaTag}}}}

	action := application.NewReadReleaseHistoryAction(store)
	for _, prerelease := range []bool{false, true} {
		result, err := action.Run(t.Context(), application.CheckRequest{CurrentVersion: version100, IncludePrerelease: prerelease})
		if err != nil || result == nil {
			t.Fatalf("result=%+v error=%v", result, err)
		}

		want := historyStableTag
		if prerelease {
			want = historyBetaTag
		}

		if !result.FromCache || result.RefreshError != "" || !result.CheckedAt.Equal(checked) || result.Available.Tag != want || store.saves != 0 {
			t.Fatalf("result=%+v store=%+v", result, store)
		}
	}
}

func TestSavedHistoryKeepsMissingAndCanceledErrors(t *testing.T) {
	t.Parallel()

	failure := errors.New("cache missing")
	store := &historyStore{loadErr: failure}

	action := application.NewReadReleaseHistoryAction(store)
	if _, err := action.Run(t.Context(), application.CheckRequest{CurrentVersion: version100}); !errors.Is(err, failure) {
		t.Fatalf("read error=%v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := action.Run(ctx, application.CheckRequest{CurrentVersion: version100}); !errors.Is(err, context.Canceled) || store.loads != 1 {
		t.Fatalf("cancel error=%v loads=%d", err, store.loads)
	}
}
