// Package garmin is the high-level API over one connected watch: files,
// settings, apps, music, software updates and the Garmin Connect sync.
package garmin

import (
	"context"
	"crypto/rand"
	"fmt"
	"path"
	"strings"
	"sync"
	"unicode"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
)

// Service is the high-level API over one connected watch.
type Service struct {
	dev     device.Device
	profile *fit.Profile
	know    *Knowledge

	// UpdatesURL is the software update service to ask; DefaultUpdatesURL
	// unless a test points it at a fake server.
	UpdatesURL string

	mu              sync.Mutex
	cache           map[string][]byte
	schemas         map[string]*ciqstore.Schema
	cacheGeneration uint64
	reconnectCount  int
}

// New wraps a device.
func New(dev device.Device) *Service {
	return &Service{
		dev:        dev,
		profile:    fit.MustProfile(),
		know:       defaultKnowledge(),
		UpdatesURL: DefaultUpdatesURL,
		cache:      map[string][]byte{},
		schemas:    map[string]*ciqstore.Schema{},
	}
}

// Profile returns the FIT profile.
func (s *Service) Profile() *fit.Profile { return s.profile }

// Knowledge returns an independent snapshot of notes about undocumented fields.
func (s *Service) Knowledge() *Knowledge { return s.know.snapshot() }

// reconnecter is implemented by devices that reopen themselves after an unplug.
type reconnecter interface{ Reconnects() int }

func (s *Service) invalidate(p string) {
	s.mu.Lock()
	s.cacheGeneration++
	delete(s.cache, strings.ToLower(p))
	s.mu.Unlock()
}

// Refresh drops cached file contents.
func (s *Service) Refresh() {
	s.mu.Lock()
	s.cacheGeneration++
	s.cache = map[string][]byte{}
	s.mu.Unlock()
}

// Close releases the device.
func (s *Service) Close() error { return s.dev.Close() }

// DeviceXML reads and parses GarminDevice.xml.
func (s *Service) DeviceXML(ctx context.Context) (*DeviceXML, error) {
	b, err := s.read(ctx, PathDeviceXML, true)
	if err != nil {
		return nil, err
	}

	return ParseDeviceXML(b)
}

// Summary describes the connected watch.
type Summary struct {
	Device      device.Info     `json:"device"`
	Model       string          `json:"model"`
	PartNumber  string          `json:"partNumber"`
	UnitID      uint64          `json:"unitId"`
	Software    string          `json:"software"`
	AppCount    int             `json:"appCount"`
	NewFiles    int             `json:"pendingNewFiles"`
	ProfileVers string          `json:"fitProfile"`
	Storage     *device.Storage `json:"storage,omitempty"`
}

// Summary gathers the headline facts about the watch.
func (s *Service) Summary(ctx context.Context) (*Summary, error) {
	dx, err := s.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	sum := &Summary{Device: s.Info(), Model: dx.Description, PartNumber: dx.PartNumber, UnitID: dx.UnitID, Software: dx.SoftwareVersionString(), ProfileVers: s.profile.Version}
	if apps, err := s.Apps(ctx); err == nil {
		sum.AppCount = len(apps)
	}

	if entries, err := s.List(ctx, PathNewFiles); err == nil {
		sum.NewFiles = len(entries)
	}

	if st, err := s.StorageInfo(ctx); err == nil && st.CapacityBytes > 0 {
		sum.Storage = st
	}

	return sum, nil
}

// Apps lists installed applications.
func (s *Service) Apps(ctx context.Context) ([]App, error) {
	b, err := s.read(ctx, PathAppsConfig, true)
	if err != nil {
		return nil, err
	}

	return ParseAppsConfig(b)
}

// Settings reads Settings.fit.
func (s *Service) Settings(ctx context.Context) (*SettingsView, error) {
	b, err := s.read(ctx, PathSettings, true)
	if err != nil {
		return nil, err
	}

	f, err := fit.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PathSettings, err)
	}

	return &SettingsView{Path: PathSettings, Messages: s.profile.Describe(f), Knowledge: s.Knowledge()}, nil
}

// DecodeFIT decodes any FIT file on the watch through the profile.
func (s *Service) DecodeFIT(ctx context.Context, p string) ([]fit.NamedMessage, error) {
	b, err := s.read(ctx, p, false)
	if err != nil {
		return nil, err
	}

	f, err := fit.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}

	return s.profile.Describe(f), nil
}

// AppSettings is the settings of one Connect IQ app.
type AppSettings struct {
	App          App              `json:"app"`
	Path         string           `json:"path"`
	Settings     ciq.Dict         `json:"-"`
	Values       map[string]any   `json:"values"`
	Order        []string         `json:"order"`
	Items        []AppSettingItem `json:"items"`
	SchemaSource string           `json:"schemaSource,omitempty"`
	SchemaError  string           `json:"schemaError,omitempty"`
	Hints        *AppHints        `json:"hints,omitempty"`
}

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

func newUUID() string {
	var b [16]byte

	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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
