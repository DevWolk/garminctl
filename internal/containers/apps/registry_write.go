package apps

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// PathAppsIn is the pending app registry, encoded like OUT.BIN. The watch
// applies and deletes it when the USB session ends. Field 4 controls entry
// visibility; see docs/formats.md.
const PathAppsIn = "GARMIN/Apps/IN.BIN"

// PathAppsConfig is the current installed registry.
const PathAppsConfig = watchsession.PathAppsConfig

func pbAppendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}

	return append(b, byte(v))
}

func pbEncode(fields []pbField) []byte {
	var b []byte
	for _, f := range fields {
		b = pbAppendVarint(b, f.num<<3|f.wire)
		switch f.wire {
		case 0:
			b = pbAppendVarint(b, f.varint)
		case 2:
			b = pbAppendVarint(b, uint64(len(f.data)))
			b = append(b, f.data...)
		default:
			b = append(b, f.data...)
		}
	}

	return b
}

// appEdit changes one app entry of the registry; returning nil drops it.
type appEdit func(app App, fields []pbField) ([]pbField, error)

// rewriteApps re-encodes an OUT.BIN image, letting edit change each app entry.
// Everything else is emitted byte for byte.
func rewriteApps(out []byte, edit appEdit) ([]byte, error) {
	root, err := pbFields(out)
	if err != nil {
		return nil, err
	}

	for i, f := range root {
		if f.num != 3 || f.wire != 2 {
			continue
		}

		cfg, err := pbFields(f.data)
		if err != nil {
			return nil, err
		}

		for j, c := range cfg {
			if c.num != 2 || c.wire != 2 {
				continue
			}

			list, err := rewriteAppList(c.data, edit)
			if err != nil {
				return nil, err
			}

			cfg[j] = pbField{num: 2, wire: 2, data: list}
		}

		root[i] = pbField{num: 3, wire: 2, data: pbEncode(cfg)}
	}

	return pbEncode(root), nil
}

// rewriteAppList re-encodes one list message, applying edit to its App entries.
func rewriteAppList(data []byte, edit appEdit) ([]byte, error) {
	list, err := pbFields(data)
	if err != nil {
		return nil, err
	}

	var kept []pbField

	for _, e := range list {
		if e.num != 3 || e.wire != 2 {
			kept = append(kept, e)
			continue
		}

		app, err := parseApp(e.data)
		if err != nil {
			return nil, err
		}

		af, err := pbFields(e.data)
		if err != nil {
			return nil, err
		}

		nf, err := edit(app, af)
		if err != nil {
			return nil, err
		}

		if nf == nil {
			continue
		}

		kept = append(kept, pbField{num: 3, wire: 2, data: pbEncode(nf)})
	}

	return pbEncode(kept), nil
}

// matchesApp reports whether ref names the app (name, file id or store id).
func matchesApp(app App, ref string) bool {
	return strings.EqualFold(app.Name, ref) || strings.EqualFold(app.FileName, ref) || strings.EqualFold(app.ID, ref)
}

// setHiddenFlag writes field 4 of an app entry and reports whether it exists.
func setHiddenFlag(f []pbField, hidden bool) bool {
	found := false

	for i := range f {
		if f[i].num == 4 && f[i].wire == 0 {
			f[i].varint = 0
			if hidden {
				f[i].varint = 1
			}

			found = true
		}
	}

	return found
}

// VisibilityAction hides an activity, glance or app from the watch's lists or
// shows it again, by writing IN.BIN with that one flag changed. A pending
// IN.BIN is used as the base, so several changes can be queued before the
// unplug that applies them. A private OUT.BIN backup in the cache directory must
// be written and read back successfully before IN.BIN is changed.
// ref matches the app's name, file id or store id.
type VisibilityAction struct{}

// Run stages one visibility change using the caller's leased Session.
func (VisibilityAction) Run(ctx context.Context, sess *watchsession.Session, ref string, hidden bool) (backup string, err error) {
	out, err := sess.ReadThrough(ctx, PathAppsConfig, false)
	if err != nil {
		return "", err
	}

	// Only a missing IN.BIN means an empty queue; any other read failure would
	// silently drop the changes already queued there.
	pending, err := sess.ReadFile(ctx, PathAppsIn)
	if err != nil && !isNotFound(err) {
		return "", fmt.Errorf("read pending %s: %w", PathAppsIn, err)
	}

	base := out
	if len(pending) > 0 {
		base = pending
	} else {
		pending = nil
	}

	identity, err := rewriteApps(base, func(_ App, f []pbField) ([]pbField, error) { return f, nil })
	if err != nil {
		return "", err
	}

	if string(identity) != string(base) {
		return "", errors.New("the app registry does not re-encode byte for byte; refusing to write one the watch might not recognise")
	}

	hits := 0

	in, err := rewriteApps(base, func(app App, f []pbField) ([]pbField, error) {
		if !matchesApp(app, ref) {
			return f, nil
		}

		hits++

		if !setHiddenFlag(f, hidden) {
			return nil, fmt.Errorf("%s has no flag field in the registry", app.Name)
		}

		return f, nil
	})
	if err != nil {
		return "", err
	}

	if hits == 0 {
		return "", fmt.Errorf("%q is not in the watch's app registry", ref)
	}

	if hits > 1 {
		return "", fmt.Errorf("%q matches %d registry entries (a glance and an app share the name); use the store id", ref, hits)
	}

	backup, err = backupRegistry(out, pending)
	if err != nil {
		return "", fmt.Errorf("back up app registry before staging changes: %w", err)
	}

	if err := sess.WriteFile(ctx, PathAppsIn, in); err != nil {
		return backup, err
	}

	return backup, nil
}

// isNotFound reports a missing path. Every transport maps absence to
// device.ErrNotFound; matching the message text would also accept transport
// failures whose text contains "not found" and silently drop a queued IN.BIN.
func isNotFound(err error) bool {
	return errors.Is(err, device.ErrNotFound)
}

// PendingRegistryAction lists what IN.BIN will change when the watch is unplugged.
type PendingRegistryAction struct{}

// Run compares the current and pending registries.
func (PendingRegistryAction) Run(ctx context.Context, sess *watchsession.Session) ([]AppChange, error) {
	pending, err := sess.ReadFile(ctx, PathAppsIn)
	if err != nil {
		if isNotFound(err) {
			return []AppChange{}, nil
		}

		return nil, err
	}

	out, err := sess.ReadThrough(ctx, PathAppsConfig, true)
	if err != nil {
		return nil, err
	}

	cur, err := ParseAppsConfig(out)
	if err != nil {
		return nil, err
	}

	next, err := ParseAppsConfig(pending)
	if err != nil {
		return nil, err
	}

	key := func(a App) string { return a.ID + "/" + a.TypeName }

	was := map[string]App{}
	for _, a := range cur {
		was[key(a)] = a
	}

	changes := []AppChange{}

	for _, a := range next {
		if w, ok := was[key(a)]; ok && w.Hidden != a.Hidden {
			changes = append(changes, AppChange{ID: a.ID, Name: a.Name, TypeName: a.TypeName, Hidden: a.Hidden})
		}
	}

	return changes, nil
}

// ReadRegistryAction reads the current installed application registry.
type ReadRegistryAction struct{}

// Run retains the existing cached raw read policy.
func (ReadRegistryAction) Run(ctx context.Context, sess *watchsession.Session) ([]App, error) {
	return readRegistry(ctx, sess)
}

func readRegistry(ctx context.Context, sess *watchsession.Session) ([]App, error) {
	b, err := sess.ReadThrough(ctx, PathAppsConfig, true)
	if err != nil {
		return nil, err
	}

	return ParseAppsConfig(b)
}
