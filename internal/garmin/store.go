package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/containers/store"
)

// StoreView is the compatibility name for the store-owned catalog view.
type StoreView = store.View

// StoreViewApp is the compatibility name for a decorated store app.
type StoreViewApp = store.App

// StoreDetail is the compatibility name for an app with reviews.
type StoreDetail = store.Detail

func (s *Service) storeRegistry() store.InstalledApps {
	if s == nil {
		return nil
	}

	return s.Apps
}

// BrowseStore delegates to the catalog owner without requiring a connected watch.
func (s *Service) BrowseStore(ctx context.Context, part string, q ciqstore.StoreQuery) (*StoreView, error) {
	return store.NewBrowseAction(s.storeRegistry()).Run(ctx, part, q)
}

// StoreApp delegates to the catalog owner with optional installed-app decoration.
func (s *Service) StoreApp(ctx context.Context, part, id string) (*StoreDetail, error) {
	return store.NewDetailAction(s.storeRegistry()).Run(ctx, part, id)
}
