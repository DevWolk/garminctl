// Package garmin is the high-level API over one connected watch: files,
// settings, apps, music, software updates and the Garmin Connect sync.
package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/settings"
	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Service is the high-level API over one connected watch.
type Service struct {
	profile *fit.Profile

	// UpdatesURL is the software update service to ask; DefaultUpdatesURL
	// unless a test points it at a fake server.
	UpdatesURL string

	session *watchsession.Session
	schemas apps.SchemaCache
}

// New wraps a device.
func New(dev device.Device) *Service { return NewFromSession(watchsession.New(dev)) }

// NewFromSession adds feature caches/profile to an existing Session without
// taking transport ownership or creating another admission/close worker.
func NewFromSession(session *watchsession.Session) *Service {
	return &Service{
		profile:    fit.MustProfile(),
		UpdatesURL: DefaultUpdatesURL,
		session:    session,
	}
}

// Profile returns the FIT profile.
func (s *Service) Profile() *fit.Profile { return s.profile }

// Knowledge returns an independent snapshot of notes about undocumented fields.
func (s *Service) Knowledge() *Knowledge { return settings.DefaultKnowledge() }

func (s *Service) invalidate(p string) {
	s.schemas.Reset(s.session.Invalidate(p))
}

// Refresh drops cached file contents.
func (s *Service) Refresh() {
	s.schemas.Reset(s.session.Refresh())
}

// Close releases the device.
func (s *Service) Close() error { return s.session.Close() }

// DeviceXML reads and parses GarminDevice.xml.
func (s *Service) DeviceXML(ctx context.Context) (*DeviceXML, error) {
	return s.session.DeviceXML(ctx)
}

// Summary preserves the watch summary model during facade migration.
type Summary = watch.Summary

// Summary gathers the headline facts about the watch.
func (s *Service) Summary(ctx context.Context) (*Summary, error) {
	return watch.NewSummaryAction(s.profile, func(ctx context.Context, _ *watchsession.Session) (int, error) {
		apps, err := s.Apps(ctx)
		return len(apps), err
	}).Run(ctx, s.session)
}

// Apps lists installed applications.
func (s *Service) Apps(ctx context.Context) ([]App, error) {
	return (apps.ReadRegistryAction{}).Run(ctx, s.session)
}

// Settings reads Settings.fit.
func (s *Service) Settings(ctx context.Context) (*SettingsView, error) {
	return settings.NewReadAction(s.profile).Run(ctx, s.session)
}

// DecodeFIT decodes any FIT file on the watch through the profile.
func (s *Service) DecodeFIT(ctx context.Context, p string) ([]fit.NamedMessage, error) {
	return watch.NewDecodeAction(s.profile).Run(ctx, s.session, p)
}

// AppSettings preserves the app settings response during facade migration.
type AppSettings = apps.AppSettings

// FindApp delegates exact/partial store-app lookup.
func (s *Service) FindApp(ctx context.Context, ref string) (*App, error) {
	return (apps.FindAction{}).Run(ctx, s.session, ref)
}

// AppSettings reads settings through the apps owner and this facade's cache.
func (s *Service) AppSettings(ctx context.Context, ref string) (*AppSettings, error) {
	return apps.NewReadSettingsAction(&s.schemas).Run(ctx, s.session, ref)
}

// SetAppSettings delegates validation, write and readback with the same cache.
func (s *Service) SetAppSettings(ctx context.Context, ref string, updates map[string]any) (*AppSettings, error) {
	return apps.NewApplySettingsAction(&s.schemas).Run(ctx, s.session, ref, updates)
}

func newUUID() string { return watchsession.NewFileID() }
