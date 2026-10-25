package garmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/paths"
)

// PathAppsIn is the pending app registry, encoded like OUT.BIN. The watch
// applies and deletes it when the USB session ends. Field 4 controls entry
// visibility; see docs/formats.md.
const PathAppsIn = "GARMIN/Apps/IN.BIN"

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

// backupRegistry keeps a copy of OUT.BIN next to the caches and returns its
// path, or "" when it could not be written.
func backupRegistry(out []byte) string {
	dir := paths.Cache()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}

	backup := filepath.Join(dir, "OUT.BIN-"+time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, out, 0o600); err != nil {
		return ""
	}

	return backup
}

// SetAppHidden hides an activity, glance or app from the watch's lists or
// shows it again, by writing IN.BIN with that one flag changed. A pending
// IN.BIN is used as the base, so several changes can be queued before the
// unplug that applies them. An OUT.BIN backup is attempted in the cache directory.
// ref matches the app's name, file id or store id.
func (s *Service) SetAppHidden(ctx context.Context, ref string, hidden bool) (backup string, err error) {
	out, err := s.read(ctx, PathAppsConfig, false)
	if err != nil {
		return "", err
	}

	base := out
	if pending, err := s.dev.Read(ctx, PathAppsIn); err == nil && len(pending) > 0 {
		base = pending
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

	backup = backupRegistry(out)

	if err := s.dev.Write(ctx, PathAppsIn, in); err != nil {
		return backup, err
	}

	return backup, nil
}

// AppChange is one difference between the registry and the pending IN.BIN.
type AppChange struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TypeName string `json:"typeName"`
	Hidden   bool   `json:"hidden"` // the value that applies on unplug
}

// PendingAppChanges lists what IN.BIN will change when the watch is unplugged.
func (s *Service) PendingAppChanges(ctx context.Context) ([]AppChange, error) {
	pending, err := s.dev.Read(ctx, PathAppsIn)
	if err != nil {
		if errors.Is(err, device.ErrNotFound) || strings.Contains(err.Error(), "not found") {
			return []AppChange{}, nil
		}

		return nil, err
	}

	out, err := s.read(ctx, PathAppsConfig, true)
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

// CancelAppChanges deletes the pending IN.BIN.
func (s *Service) CancelAppChanges(ctx context.Context) error {
	return s.dev.Delete(ctx, PathAppsIn)
}
