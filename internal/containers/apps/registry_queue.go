package apps

import (
	"context"
	"fmt"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// RestoreRegistryAction validates and stages a saved registry unchanged.
type RestoreRegistryAction struct{}

// Run borrows the caller's Session. Source names the input in validation errors;
// the caller reads that source, and this Action performs no host file access.
func (RestoreRegistryAction) Run(ctx context.Context, sess *watchsession.Session, source string, data []byte) error {
	if _, err := ParseAppsConfig(data); err != nil {
		return fmt.Errorf("%s is not an app registry: %w", source, err)
	}

	return sess.WriteFile(ctx, PathAppsIn, data)
}

// CancelRegistryAction removes the pending registry without changing OUT.BIN.
type CancelRegistryAction struct{}

// Run preserves the transport's error when the pending registry cannot be removed.
func (CancelRegistryAction) Run(ctx context.Context, sess *watchsession.Session) error {
	return sess.DeleteFile(ctx, PathAppsIn)
}
