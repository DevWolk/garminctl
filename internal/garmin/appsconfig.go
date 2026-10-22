package garmin

import (
	"encoding/hex"
	"errors"
	"fmt"
)

// App is an installed application as listed in GARMIN/Apps/OUT.BIN, the
// protobuf the watch writes for Garmin Express.
type App struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	TypeName string `json:"typeName"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Version  int    `json:"version,omitempty"`
	FileName string `json:"fileName,omitempty"` // Connect IQ apps only, e.g. G5RG4154
	Size     int    `json:"size,omitempty"`
	NativeID int    `json:"nativeId,omitempty"` // built-in apps only
	Native   bool   `json:"native"`
}

var appTypeNames = map[int]string{
	1: "app",
	2: "glance",
	3: "watchface",
	4: "datafield",
	5: "widget",
	6: "audio",
	7: "music",
	8: "activity",
}

// ParseAppsConfig decodes OUT.BIN.
func ParseAppsConfig(b []byte) ([]App, error) {
	root, err := pbFields(b)
	if err != nil {
		return nil, err
	}
	// Layout: root.3 -> config.2 -> list{1: id, 2: count, repeated 3: App}.
	var apps []App
	collect := func(list []pbField) error {
		for _, e := range list {
			if e.num != 3 || e.wire != 2 {
				continue
			}
			app, err := parseApp(e.data)
			if err != nil {
				return err
			}
			if app.Name != "" {
				apps = append(apps, app)
			}
		}
		return nil
	}
	for _, f := range root {
		if f.num != 3 || f.wire != 2 {
			continue
		}
		cfg, err := pbFields(f.data)
		if err != nil {
			return nil, err
		}
		for _, c := range cfg {
			if c.num != 2 || c.wire != 2 {
				continue
			}
			list, err := pbFields(c.data)
			if err != nil {
				return nil, err
			}
			if err := collect(list); err != nil {
				return nil, err
			}
		}
	}
	if apps == nil {
		return nil, errors.New("apps config: no application entries")
	}
	return apps, nil
}

func parseApp(b []byte) (App, error) {
	fs, err := pbFields(b)
	if err != nil {
		return App{}, err
	}
	var a App
	for _, f := range fs {
		switch f.num {
		case 1:
			if len(f.data) == 16 {
				h := hex.EncodeToString(f.data)
				a.ID = h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
			}
		case 2:
			a.Type = int(f.varint)
		case 3:
			a.Name = string(f.data)
		case 4:
			a.Enabled = f.varint != 0
		case 5:
			a.Version = int(f.varint)
		case 6:
			a.FileName = string(f.data)
		case 7:
			a.Size = int(f.varint)
		case 8:
			a.NativeID = int(f.varint)
		}
	}
	a.Native = a.FileName == ""
	if n, ok := appTypeNames[a.Type]; ok {
		a.TypeName = n
	} else {
		a.TypeName = fmt.Sprintf("type%d", a.Type)
	}
	return a, nil
}

type pbField struct {
	num    int
	wire   int
	varint uint64
	data   []byte
}

func pbVarint(b []byte, p int) (uint64, int, error) {
	var v uint64
	var shift uint
	for {
		if p >= len(b) {
			return 0, p, errors.New("protobuf: truncated varint")
		}
		c := b[p]
		p++
		v |= uint64(c&0x7F) << shift
		if c < 0x80 {
			return v, p, nil
		}
		shift += 7
		if shift > 63 {
			return 0, p, errors.New("protobuf: varint too long")
		}
	}
}

func pbFields(b []byte) ([]pbField, error) {
	var out []pbField
	p := 0
	for p < len(b) {
		tag, np, err := pbVarint(b, p)
		if err != nil {
			return nil, err
		}
		p = np
		f := pbField{num: int(tag >> 3), wire: int(tag & 7)}
		switch f.wire {
		case 0:
			f.varint, p, err = pbVarint(b, p)
			if err != nil {
				return nil, err
			}
		case 1:
			if p+8 > len(b) {
				return nil, errors.New("protobuf: truncated fixed64")
			}
			f.data = b[p : p+8]
			p += 8
		case 2:
			n, np, err := pbVarint(b, p)
			if err != nil {
				return nil, err
			}
			p = np
			if p+int(n) > len(b) {
				return nil, errors.New("protobuf: truncated bytes")
			}
			f.data = b[p : p+int(n)]
			p += int(n)
		case 5:
			if p+4 > len(b) {
				return nil, errors.New("protobuf: truncated fixed32")
			}
			f.data = b[p : p+4]
			p += 4
		default:
			return nil, fmt.Errorf("protobuf: unsupported wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}
