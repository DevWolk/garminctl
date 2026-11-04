package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/store"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// StoreRegistry supplies the apps-owned registry within the caller's lease.
// The returned reader must not escape that Session's operation scope.
func StoreRegistry(sess *watchsession.Session) store.InstalledApps {
	if sess == nil {
		return nil
	}

	return func(ctx context.Context) ([]apps.App, error) {
		return (apps.ReadRegistryAction{}).Run(ctx, sess)
	}
}
