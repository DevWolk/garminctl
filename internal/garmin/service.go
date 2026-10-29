// Package garmin is the high-level API over one connected watch: files,
// settings, apps, music, software updates and the Garmin Connect sync.
package garmin

import (
	"context"
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/ciqstore"
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
	schemas appSchemaCache
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
	s.schemas.reset(s.session.Invalidate(p))
}

// Refresh drops cached file contents.
func (s *Service) Refresh() {
	s.schemas.reset(s.session.Refresh())
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

// FindApp resolves an app by file name (G5RG4154), store id, or name
// (case-insensitive, "Easy+" or "easy").
func (s *Service) FindApp(ctx context.Context, ref string) (*App, error) {
	apps, err := s.Apps(ctx)
	if err != nil {
		return nil, err
	}

	ref = strings.TrimSpace(ref)

	var partial *App

	for i := range apps {
		a := &apps[i]
		if strings.EqualFold(a.FileName, ref) || strings.EqualFold(a.ID, ref) || strings.EqualFold(a.Name, ref) {
			if a.FileName != "" {
				return a, nil
			}
		}

		if a.FileName != "" && partial == nil && strings.Contains(strings.ToLower(a.Name), strings.ToLower(ref)) {
			partial = a
		}
	}

	if partial != nil {
		return partial, nil
	}

	return nil, fmt.Errorf("no Connect IQ app matches %q", ref)
}

// AppSettings reads the .SET file of an app.
func (s *Service) AppSettings(ctx context.Context, ref string) (*AppSettings, error) {
	app, err := s.FindApp(ctx, ref)
	if err != nil {
		return nil, err
	}

	p := path.Join(PathAppSettings, app.FileName+".SET")

	b, err := s.read(ctx, p, false)
	if err != nil {
		return nil, err
	}

	v, err := ciq.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}

	d, ok := v.(ciq.Dict)
	if !ok {
		return nil, fmt.Errorf("%s: settings are a %T, not a dictionary", p, v)
	}

	as := &AppSettings{App: *app, Path: p, Settings: d, Values: map[string]any{}, Hints: AppHintsFor(app.ID)}
	for _, pair := range d {
		k := fmt.Sprint(pair.Key)
		as.Order = append(as.Order, k)
		as.Values[k] = ciq.ToJSONValue(pair.Value)
	}

	s.decorate(ctx, as)

	return as, nil
}

// SetAppSettings updates keys of an app's .SET file and writes it back.
// Values are coerced to the type of the existing value.
func (s *Service) SetAppSettings(ctx context.Context, ref string, updates map[string]any) (*AppSettings, error) {
	as, err := s.AppSettings(ctx, ref)
	if err != nil {
		return nil, err
	}

	sc, _ := s.appSchema(ctx, &as.App)
	for k, v := range updates {
		cur, ok := as.Settings.Get(k)
		if !ok {
			return nil, fmt.Errorf("%s has no setting %q", as.App.Name, k)
		}

		var (
			nv  any
			err error
		)
		if st, found := schemaSetting(sc, k); found {
			nv, err = convertWithSchema(st, cur, v)
		} else {
			nv, err = ciq.Coerce(cur, v)
		}

		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}

		as.Settings.Set(k, nv)
	}

	data, err := ciq.Encode(as.Settings)
	if err != nil {
		return nil, err
	}

	if err := s.WriteFile(ctx, as.Path, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", as.Path, err)
	}

	s.invalidate(as.Path)

	return s.AppSettings(ctx, ref)
}

func newUUID() string { return watchsession.NewFileID() }

func schemaSetting(sc *ciqstore.Schema, key string) (*ciqstore.Setting, bool) {
	if sc == nil {
		return nil, false
	}

	return sc.Get(key)
}

// titleCase upper-cases the first letter of every word and lower-cases the
// rest; underscores count as word breaks ("trail_running" -> "Trail Running").
func titleCase(s string) string {
	var b strings.Builder

	startWord := true

	for _, r := range strings.ReplaceAll(s, "_", " ") {
		switch {
		case unicode.IsSpace(r):
			startWord = true

			b.WriteRune(r)
		case startWord:
			startWord = false

			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}

	return b.String()
}
