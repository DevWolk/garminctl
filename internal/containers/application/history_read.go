package application

import (
	"context"
	"errors"
	"fmt"
)

// ReadReleaseHistoryAction reads saved metadata without contacting a feed.
type ReadReleaseHistoryAction struct{ store ReleaseHistoryStore }

// NewReadReleaseHistoryAction borrows the same store as the explicit checker.
func NewReadReleaseHistoryAction(store ReleaseHistoryStore) *ReadReleaseHistoryAction {
	return &ReadReleaseHistoryAction{store: store}
}

// Run reapplies channel policy to saved metadata and never refreshes the feed.
func (a *ReadReleaseHistoryAction) Run(ctx context.Context, request CheckRequest) (*ReleaseHistoryResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if a.store == nil {
		return nil, errors.New("application release history is not configured")
	}

	current := semanticVersion(request.CurrentVersion)
	if current == "" {
		return nil, fmt.Errorf("invalid current application version %q", request.CurrentVersion)
	}

	return readReleaseHistory(ctx, a.store, request, current, nil)
}
