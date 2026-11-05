package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/containers/apps"
)

// AppSettingItem preserves the app settings item model during migration.
type AppSettingItem = apps.AppSettingItem

// AppSchema delegates app lookup and cached schema loading to apps.
func (s *Service) AppSchema(ctx context.Context, ref string) (*ciqstore.Schema, error) {
	return apps.NewSchemaAction(s.schemas).Run(ctx, s.session, ref)
}
