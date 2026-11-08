package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ReleaseSnapshot retains unfiltered metadata and its successful check time.
// It grants no permission to download or install a package.
type ReleaseSnapshot struct {
	CheckedAt time.Time
	Releases  []Release
}

// ReleaseHistoryStore supplies caller-owned persistence without transport policy.
type ReleaseHistoryStore interface {
	Load(context.Context) (*ReleaseSnapshot, error)
	Save(context.Context, ReleaseSnapshot) error
}

// ReleaseHistoryResult distinguishes a fresh check from stale cached metadata.
// A successful fresh check may carry CacheError when persistence failed.
type ReleaseHistoryResult struct {
	CheckResult
	CheckedAt    time.Time `json:"checkedAt"`
	FromCache    bool      `json:"fromCache"`
	RefreshError string    `json:"refreshError,omitempty"`
	CacheError   string    `json:"cacheError,omitempty"`
}

// CheckReleaseHistoryAction checks on request, falling back explicitly on error.
// It owns no background checks; cancellation never becomes a cached success.
type CheckReleaseHistoryAction struct {
	feed  ReleaseFeed
	store ReleaseHistoryStore
}

// NewCheckReleaseHistoryAction borrows the feed and isolated metadata store.
func NewCheckReleaseHistoryAction(feed ReleaseFeed, store ReleaseHistoryStore) *CheckReleaseHistoryAction {
	return &CheckReleaseHistoryAction{feed: feed, store: store}
}

// Run applies the current channel/version policy to fresh or cached metadata.
// Refresh errors remain visible in cached results; no valid cache means error.
func (a *CheckReleaseHistoryAction) Run(ctx context.Context, request CheckRequest) (*ReleaseHistoryResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	current := semanticVersion(request.CurrentVersion)
	if current == "" {
		return nil, fmt.Errorf("invalid current application version %q", request.CurrentVersion)
	}

	if a.feed == nil || a.store == nil {
		return nil, errors.New("application release history is not configured")
	}

	releases, err := a.feed.Releases(ctx)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}

	if errors.Is(err, context.Canceled) {
		return nil, err
	}

	if err != nil {
		return a.cached(ctx, request, current, err)
	}

	selected, err := selectReleaseSnapshot(ctx, request, current, releases)
	if err != nil {
		return nil, err
	}

	snapshot := ReleaseSnapshot{CheckedAt: time.Now().UTC(), Releases: slices.Clone(releases)}

	result := &ReleaseHistoryResult{CheckResult: *selected, CheckedAt: snapshot.CheckedAt}
	if err := a.store.Save(ctx, snapshot); err != nil {
		result.CacheError = err.Error()
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (a *CheckReleaseHistoryAction) cached(ctx context.Context, request CheckRequest, current string, refreshErr error) (*ReleaseHistoryResult, error) {
	snapshot, err := a.store.Load(ctx)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}

	if err == nil && (snapshot == nil || snapshot.CheckedAt.IsZero() || snapshot.Releases == nil) {
		err = errors.New("incomplete application release history")
	}

	if err != nil {
		return nil, errors.Join(refreshErr, fmt.Errorf("load application release history: %w", err))
	}

	selected, err := selectReleaseSnapshot(ctx, request, current, snapshot.Releases)
	if err != nil {
		return nil, errors.Join(refreshErr, err)
	}

	return &ReleaseHistoryResult{CheckResult: *selected, CheckedAt: snapshot.CheckedAt, FromCache: true, RefreshError: refreshErr.Error()}, nil
}
