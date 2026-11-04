package apps

import (
	"context"
	"fmt"
	"path"

	"github.com/DevWolk/garminctl/internal/ciq"
	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// ReadSettingsAction uses the current Session's feature schema cache.
type ReadSettingsAction struct{ cache *SchemaCache }

// NewReadSettingsAction borrows the caller-owned feature cache.
func NewReadSettingsAction(cache *SchemaCache) *ReadSettingsAction {
	return &ReadSettingsAction{cache: cache}
}

// Run reads and decorates the app's ordered settings dictionary.
func (a *ReadSettingsAction) Run(ctx context.Context, sess *watchsession.Session, ref string) (*AppSettings, error) {
	return readSettings(ctx, sess, a.cache, ref)
}

// ApplySettingsAction shares the same feature cache as reads and schema lookup.
type ApplySettingsAction struct{ cache *SchemaCache }

// NewApplySettingsAction borrows the caller-owned feature cache.
func NewApplySettingsAction(cache *SchemaCache) *ApplySettingsAction {
	return &ApplySettingsAction{cache: cache}
}

// Run validates, writes and reads back edits without owning a Session.
func (a *ApplySettingsAction) Run(ctx context.Context, sess *watchsession.Session, ref string, updates map[string]any) (*AppSettings, error) {
	return applySettings(ctx, sess, a.cache, ref, updates)
}

// AppSettings reads the .SET file of an app.
func readSettings(ctx context.Context, sess *watchsession.Session, cache *SchemaCache, ref string) (*AppSettings, error) {
	app, err := findApp(ctx, sess, ref)
	if err != nil {
		return nil, err
	}

	p := path.Join(watchsession.PathAppSettings, app.FileName+".SET")

	b, err := sess.ReadThrough(ctx, p, false)
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

	as := &AppSettings{App: *app, Path: p, Settings: d, Values: map[string]any{}, Hints: AppHintsFor(app.ID), raw: b}
	for _, pair := range d {
		k := fmt.Sprint(pair.Key)
		as.Order = append(as.Order, k)
		as.Values[k] = ciq.ToJSONValue(pair.Value)
	}

	decorate(ctx, sess, cache, as)

	return as, nil
}

// SetAppSettings updates keys of an app's .SET file and writes it back.
// Values are coerced to the type of the existing value.
func applySettings(ctx context.Context, sess *watchsession.Session, cache *SchemaCache, ref string, updates map[string]any) (*AppSettings, error) {
	as, err := readSettings(ctx, sess, cache, ref)
	if err != nil {
		return nil, err
	}

	sc, _ := cache.Load(ctx, sess, &as.App)
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

	// The watch replaces the file in place, so a failed transfer can leave
	// the app without settings; the private copy is the only recovery path.
	backup, err := backupSettings(path.Base(as.Path), as.raw)
	if err != nil {
		return nil, fmt.Errorf("back up %s before writing: %w", as.Path, err)
	}

	if err := sess.WriteFile(ctx, as.Path, data); err != nil {
		return nil, fmt.Errorf("write %s (backup %s): %w", as.Path, backup, err)
	}

	cache.Reset(sess.Invalidate(as.Path))
	// The write changed the .SET file, not the app's schema.
	cache.Retain(ctx, sess, &as.App, sc)

	return readSettings(ctx, sess, cache, ref)
}

func schemaSetting(sc *ciqstore.Schema, key string) (*ciqstore.Setting, bool) {
	if sc == nil {
		return nil, false
	}

	return sc.Get(key)
}
