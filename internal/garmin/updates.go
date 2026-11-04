package garmin

import (
	"context"
	"net/http"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/device"
)

// DefaultUpdatesURL preserves the watch update endpoint during migration.
const DefaultUpdatesURL = updates.DefaultURL

// HTTPClient is the compatibility injection point for update checks/downloads.
// App wiring supplies this same client to extracted adapters until facade removal.
var HTTPClient = &http.Client{Timeout: 10 * time.Minute}

// SoftwareUpdate preserves the updates-owned manifest model.
type (
	SoftwareUpdate = updates.SoftwareUpdate
	// InstallResult preserves the updates-owned staging result.
	InstallResult = updates.InstallResult
	// SoftwarePart preserves the updates-owned inventory model.
	SoftwarePart = updates.SoftwarePart
	// AppUpdate preserves the updates-owned installed-app version comparison.
	AppUpdate = updates.AppUpdate
)

func (s *Service) updateConfig() updates.Config {
	return updates.Config{URL: s.UpdatesURL, Client: HTTPClient}
}

// CheckSoftwareUpdates delegates to the watch updates owner.
func (s *Service) CheckSoftwareUpdates(ctx context.Context) ([]SoftwareUpdate, error) {
	return updates.NewCheckSoftwareAction(s.updateConfig()).Run(ctx, s.session)
}

// InstallSoftwareUpdate delegates staging and preserves shared schema invalidation.
func (s *Service) InstallSoftwareUpdate(ctx context.Context, part string) (*InstallResult, error) {
	return updates.NewInstallSoftwareAction(s.updateConfig(), s.schemas.Reset).Run(ctx, s.session, part)
}

// SoftwareInventory delegates the existing device.fit interpretation.
func (s *Service) SoftwareInventory(ctx context.Context) ([]SoftwarePart, error) {
	return (updates.InventoryAction{}).Run(ctx, s.session)
}

// CheckAppUpdates delegates app comparison through the apps registry reader.
func (s *Service) CheckAppUpdates(ctx context.Context) ([]AppUpdate, error) {
	return updates.NewCheckAppsAction(s.Apps).Run(ctx)
}

// AppFolder preserves the app-owned binary folder path during migration.
const AppFolder = apps.AppFolder

// InstallPRG delegates validation/staging and shares this Session's feature cache.
func (s *Service) InstallPRG(ctx context.Context, data []byte) (string, error) {
	return apps.NewInstallPRGAction(&s.schemas).Run(ctx, s.session, data)
}

// WriteAppFile delegates named queue/binary delivery through the same Session.
func (s *Service) WriteAppFile(ctx context.Context, name string, data []byte) (string, error) {
	return (apps.WriteFileAction{}).Run(ctx, s.session, name, data)
}

// RemoveApp delegates app-file removal with the existing shared schema cache.
func (s *Service) RemoveApp(ctx context.Context, ref string) ([]string, error) {
	return apps.NewRemoveAction(&s.schemas).Run(ctx, s.session, ref)
}

// AppFiles delegates listing through the visible/hidden capability policy.
func (s *Service) AppFiles(ctx context.Context) ([]device.Entry, error) {
	return (apps.FilesAction{}).Run(ctx, s.session)
}
