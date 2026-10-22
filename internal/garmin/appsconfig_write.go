package garmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PathAppsIn is the registry the host hands back to the watch: the same
// protobuf as OUT.BIN; the watch applies it when the USB session ends and
// then deletes it. Garmin Express and openExpress use it to toggle built-in
// activities and glances. What the per-app flag (field 4) means on this
// watch is not verified yet; see docs/formats.md.
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
		b = pbAppendVarint(b, uint64(f.num)<<3|uint64(f.wire))
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

// rewriteApps re-encodes an OUT.BIN image, letting edit change each app entry
// (return nil to drop it). Everything else is emitted byte for byte.
func rewriteApps(out []byte, edit func(app App, fields []pbField) ([]pbField, error)) ([]byte, error) {
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
			list, err := pbFields(c.data)
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
			cfg[j] = pbField{num: 2, wire: 2, data: pbEncode(kept)}
		}
		root[i] = pbField{num: 3, wire: 2, data: pbEncode(cfg)}
	}
	return pbEncode(root), nil
}

// SetAppFlag writes IN.BIN with one app's field-4 flag changed and nothing
// else, after saving a copy of OUT.BIN next to the other caches. The watch
// applies it when unplugged. ref matches the app's name, file id or store id.
func (s *Service) SetAppFlag(ctx context.Context, ref string, value bool) (backup string, err error) {
	if _, err := s.dev.Stat(ctx, PathAppsIn); err == nil {
		return "", errors.New("GARMIN/Apps/IN.BIN is already waiting for the watch; unplug it first")
	}
	out, err := s.read(ctx, PathAppsConfig, false)
	if err != nil {
		return "", err
	}
	identity, err := rewriteApps(out, func(_ App, f []pbField) ([]pbField, error) { return f, nil })
	if err != nil {
		return "", err
	}
	if string(identity) != string(out) {
		return "", errors.New("OUT.BIN does not re-encode byte for byte; refusing to write a registry the watch might not recognise")
	}
	hits := 0
	in, err := rewriteApps(out, func(app App, f []pbField) ([]pbField, error) {
		if !(strings.EqualFold(app.Name, ref) || strings.EqualFold(app.FileName, ref) || strings.EqualFold(app.ID, ref)) {
			return f, nil
		}
		hits++
		found := false
		for i := range f {
			if f[i].num == 4 && f[i].wire == 0 {
				f[i].varint = map[bool]uint64{true: 1, false: 0}[value]
				found = true
			}
		}
		if !found {
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
		return "", fmt.Errorf("%q matches %d registry entries (a glance and an app share names); use the file id or store id", ref, hits)
	}
	dir, err := os.UserCacheDir()
	if err == nil {
		dir = filepath.Join(dir, "garminctl")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			backup = filepath.Join(dir, fmt.Sprintf("OUT.BIN-%s", time.Now().Format("20060102-150405")))
			_ = os.WriteFile(backup, out, 0o600)
		}
	}
	if err := s.dev.Write(ctx, PathAppsIn, in); err != nil {
		return backup, err
	}
	return backup, nil
}
