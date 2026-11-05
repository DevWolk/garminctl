package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/apps"
)

// PathAppsIn retains the pending-registry path during facade migration.
const PathAppsIn = apps.PathAppsIn

// AppChange preserves the pending-registry result model.
type AppChange = apps.AppChange

// SetAppHidden delegates verified-backup and staging policy to apps.
func (s *Service) SetAppHidden(ctx context.Context, ref string, hidden bool) (string, error) {
	return (apps.VisibilityAction{}).Run(ctx, s.session, ref, hidden)
}

// PendingAppChanges delegates comparison of current and pending registries.
func (s *Service) PendingAppChanges(ctx context.Context) ([]AppChange, error) {
	return (apps.PendingRegistryAction{}).Run(ctx, s.session)
}

// CancelAppChanges deletes the pending registry through the same Session.
func (s *Service) CancelAppChanges(ctx context.Context) error {
	return (apps.CancelRegistryAction{}).Run(ctx, s.session)
}
