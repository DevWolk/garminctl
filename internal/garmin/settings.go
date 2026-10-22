package garmin

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"garminctl/internal/fit"
)

// Paths on the watch.
const (
	PathSettings    = "GARMIN/Settings/Settings.fit"
	PathDeviceXML   = "GARMIN/GarminDevice.xml"
	PathDeviceFIT   = "GARMIN/Device.fit"
	PathAppsConfig  = "GARMIN/Apps/OUT.BIN"
	PathAppSettings = "GARMIN/Apps/SETTINGS"
	PathNewFiles    = "GARMIN/NewFiles"
	PathSports      = "GARMIN/Sports"
)

// FileTypeSettingsChange is the file_id.type of the settings-change files
// Garmin Connect drops into GARMIN/NewFiles.
const FileTypeSettingsChange = 41

// MesgSettingsChange is the undocumented message that carries one field
// change: 0 = mesg_num, 1 = message_index, 2 = field_num, 3 = array index,
// 4 = value, 253 = timestamp.
const MesgSettingsChange = 217

// SettingsView is the decoded Settings.fit plus notes.
type SettingsView struct {
	Path      string             `json:"path"`
	Messages  []fit.NamedMessage `json:"messages"`
	Knowledge *Knowledge         `json:"knowledge"`
}

// Message returns the message with the given name or number and index.
func (v *SettingsView) Message(nameOrNum string, index int) (*fit.NamedMessage, bool) {
	for i := range v.Messages {
		m := &v.Messages[i]
		if m.Index != index {
			continue
		}
		if m.Name == nameOrNum || strconv.Itoa(int(m.Num)) == nameOrNum {
			return m, true
		}
	}
	return nil, false
}

// Change is one requested settings edit.
type Change struct {
	Mesg       string `json:"mesg"`                 // profile name ("deviceSettings") or number ("2")
	Index      int    `json:"index,omitempty"`      // message index for repeated messages (alarms, zones)
	Field      string `json:"field"`                // profile name ("backlightMode") or number ("12")
	ArrayIndex *int   `json:"arrayIndex,omitempty"` // element for array fields
	Value      any    `json:"value"`                // enum name, number, or scaled value
}

// ResolvedChange is a Change mapped to numeric identifiers.
type ResolvedChange struct {
	Change
	MesgNum  uint16 `json:"mesgNum"`
	FieldNum uint8  `json:"fieldNum"`
	Raw      int64  `json:"raw"`
	Previous any    `json:"previous,omitempty"`
}

// Resolve maps a change onto message and field numbers using the profile
// and, when available, the current settings for the previous value.
func (s *Service) Resolve(c Change, view *SettingsView) (ResolvedChange, error) {
	rc := ResolvedChange{Change: c}
	p := s.profile
	var mp *fit.MesgProfile
	if n, err := strconv.Atoi(c.Mesg); err == nil {
		rc.MesgNum = uint16(n)
		mp = p.Messages[rc.MesgNum]
	} else if m, ok := p.MessageByName(c.Mesg); ok {
		mp = m
		rc.MesgNum = m.Num
	} else {
		return rc, fmt.Errorf("unknown message %q", c.Mesg)
	}
	var fp *fit.FieldProfile
	if n, err := strconv.Atoi(c.Field); err == nil {
		rc.FieldNum = uint8(n)
		if mp != nil {
			fp = mp.Fields[rc.FieldNum]
		}
	} else if mp != nil {
		f, ok := mp.FieldByName(c.Field)
		if !ok {
			return rc, fmt.Errorf("message %s has no field %q", c.Mesg, c.Field)
		}
		fp = f
		rc.FieldNum = f.Num
	} else {
		return rc, fmt.Errorf("message %s is undocumented; address its fields by number", c.Mesg)
	}
	v, err := p.ParseValue(fp, c.Value)
	if err != nil {
		return rc, err
	}
	switch x := v.(type) {
	case string:
		return rc, fmt.Errorf("field %s.%s: string values cannot be sent as a settings change", c.Mesg, c.Field)
	case float64:
		rc.Raw = int64(x)
	default:
		iv, err := toInt64(v)
		if err != nil {
			return rc, fmt.Errorf("field %s.%s: %w", c.Mesg, c.Field, err)
		}
		rc.Raw = iv
	}
	if view != nil {
		if m, ok := view.Message(strconv.Itoa(int(rc.MesgNum)), c.Index); ok {
			if f, ok := m.FieldByName(strconv.Itoa(int(rc.FieldNum))); ok {
				rc.Previous = f.Value
				if arr, isArr := f.Raw.([]any); isArr && c.ArrayIndex != nil && *c.ArrayIndex < len(arr) {
					rc.Previous = arr[*c.ArrayIndex]
				}
			}
		}
	}
	return rc, nil
}

func toInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case float64:
		return int64(x), nil
	}
	return 0, fmt.Errorf("integer expected, got %T", v)
}

// BuildChangeFile produces a settings-change FIT file the watch applies
// from GARMIN/NewFiles. unitID and swVersion come from the device.
func BuildChangeFile(changes []ResolvedChange, unitID uint64, swVersion uint16, now time.Time) []byte {
	b := fit.NewBuilder(21217, true)
	ts := fit.Timestamp(now)
	_ = b.Write(0, []fit.FieldDef{
		{Num: 0, Size: 1, BaseType: fit.BaseEnum},
		{Num: 1, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 2, BaseType: fit.BaseUint16},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
		{Num: 3, Size: 4, BaseType: fit.BaseUint32z},
		{Num: 5, Size: 2, BaseType: fit.BaseUint16},
	}, map[uint8]any{0: FileTypeSettingsChange, 1: 1, 2: 65534, 4: ts, 3: unitID, 5: 1})
	_ = b.Write(49, []fit.FieldDef{
		{Num: 1, Size: 1, BaseType: fit.BaseUint8},
		{Num: 0, Size: 2, BaseType: fit.BaseUint16},
	}, map[uint8]any{1: 0, 0: swVersion})
	simple := []fit.FieldDef{
		{Num: 253, Size: 4, BaseType: fit.BaseUint32},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
		{Num: 0, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 1, BaseType: fit.BaseUint8},
	}
	indexed := []fit.FieldDef{
		{Num: 253, Size: 4, BaseType: fit.BaseUint32},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
		{Num: 0, Size: 2, BaseType: fit.BaseUint16},
		{Num: 1, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 1, BaseType: fit.BaseUint8},
		{Num: 3, Size: 1, BaseType: fit.BaseUint8},
	}
	for _, c := range changes {
		vals := map[uint8]any{253: ts, 4: uint32(c.Raw), 0: c.MesgNum, 2: c.FieldNum}
		if c.ArrayIndex != nil || c.Index > 0 {
			vals[1] = c.Index
			ai := 0
			if c.ArrayIndex != nil {
				ai = *c.ArrayIndex
			}
			vals[3] = ai
			_ = b.Write(MesgSettingsChange, indexed, vals)
		} else {
			_ = b.Write(MesgSettingsChange, simple, vals)
		}
	}
	return b.Bytes()
}

// ApplyResult reports what was written to the watch.
type ApplyResult struct {
	Path    string           `json:"path"`
	Bytes   int              `json:"bytes"`
	Changes []ResolvedChange `json:"changes"`
	Note    string           `json:"note"`
}

// ApplyChanges writes a settings-change file to GARMIN/NewFiles.
func (s *Service) ApplyChanges(ctx context.Context, changes []Change) (*ApplyResult, error) {
	if len(changes) == 0 {
		return nil, fmt.Errorf("no changes given")
	}
	view, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	dx, err := s.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}
	var resolved []ResolvedChange
	for _, c := range changes {
		rc, err := s.Resolve(c, view)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, rc)
	}
	data := BuildChangeFile(resolved, dx.UnitID, uint16(dx.SoftwareVersion), time.Now())
	name := fmt.Sprintf("%s/%s.FIT", PathNewFiles, newUUID())
	if err := s.dev.Write(ctx, name, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", name, err)
	}
	s.invalidate(PathSettings)
	note := "The watch applies files in GARMIN/NewFiles when the USB session ends; unplug the watch (or eject it) and check the setting."
	var managed []string
	for _, rc := range resolved {
		if s.know.ManagedByConnect(rc.MesgNum, rc.FieldNum) {
			managed = append(managed, rc.Mesg+"."+rc.Field)
		}
	}
	if len(managed) > 0 {
		note += " Warning: Garmin Connect re-sends " + strings.Join(managed, ", ") + " on every Garmin Express or phone sync and will overwrite this change unless the same value is set in Garmin Connect."
	}
	return &ApplyResult{Path: name, Bytes: len(data), Changes: resolved, Note: note}, nil
}

// SettingsFields flattens the view for display: one row per field.
type SettingsRow struct {
	Mesg      string   `json:"mesg"`
	MesgNum   uint16   `json:"mesgNum"`
	Index     int      `json:"index"`
	Field     string   `json:"field"`
	FieldNum  uint8    `json:"fieldNum"`
	Type      string   `json:"type,omitempty"`
	Units     string   `json:"units,omitempty"`
	Known     bool     `json:"known"`
	Value     any      `json:"value"`
	Raw       any      `json:"raw"`
	Note      string   `json:"note,omitempty"`
	Confident string   `json:"confidence,omitempty"`
	Options   []string `json:"options,omitempty"` // enum names when the field type is an enum
	// ManagedByConnect marks fields Garmin Connect re-sends on every sync.
	ManagedByConnect bool    `json:"managedByConnect,omitempty"`
	Scale            float64 `json:"scale,omitempty"`
	Offset           float64 `json:"offset,omitempty"`
}

// Rows returns the flattened settings with notes merged in.
func (v *SettingsView) Rows() []SettingsRow {
	return v.rows(fit.MustProfile())
}

func (v *SettingsView) rows(p *fit.Profile) []SettingsRow {
	var rows []SettingsRow
	for _, m := range v.Messages {
		mesgName := m.Name
		if mn, ok := v.Knowledge.Messages[strconv.Itoa(int(m.Num))]; ok && mesgName == "" {
			mesgName = mn.Name
		}
		if mesgName == "" {
			mesgName = "mesg" + strconv.Itoa(int(m.Num))
		}
		for _, f := range m.Fields {
			r := SettingsRow{Mesg: mesgName, MesgNum: m.Num, Index: m.Index, Field: f.Name, FieldNum: f.Num, Type: f.Type, Units: f.Units, Known: f.Known, Value: f.Value, Raw: f.Raw}
			key := strconv.Itoa(int(m.Num)) + "." + strconv.Itoa(int(f.Num))
			if n, ok := v.Knowledge.Fields[key]; ok {
				if r.Field == "" {
					r.Field = n.Name
				}
				r.Note = n.Note
				r.Confident = n.Confidence
			}
			if r.Field == "" {
				r.Field = "field" + strconv.Itoa(int(f.Num))
			}
			r.ManagedByConnect = v.Knowledge.ManagedByConnect(m.Num, f.Num)
			if mp, ok := p.Messages[m.Num]; ok {
				if fp, ok := mp.Fields[f.Num]; ok {
					r.Options = enumOptions(p, fp.Type)
					if fp.Scale != 1 || fp.Offset != 0 {
						r.Scale, r.Offset = fp.Scale, fp.Offset
					}
				}
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// ParseChangeSpec parses "mesg.field=value", "mesg[idx].field=value" or
// "mesg.field[arr]=value" as used on the command line.
func ParseChangeSpec(spec string) (Change, error) {
	eq := strings.Index(spec, "=")
	if eq < 0 {
		return Change{}, fmt.Errorf("expected mesg.field=value, got %q", spec)
	}
	left, value := spec[:eq], spec[eq+1:]
	dot := strings.Index(left, ".")
	if dot < 0 {
		return Change{}, fmt.Errorf("expected mesg.field, got %q", left)
	}
	c := Change{Mesg: left[:dot], Field: left[dot+1:], Value: value}
	if i := strings.Index(c.Mesg, "["); i >= 0 && strings.HasSuffix(c.Mesg, "]") {
		n, err := strconv.Atoi(c.Mesg[i+1 : len(c.Mesg)-1])
		if err != nil {
			return Change{}, fmt.Errorf("bad message index in %q", left)
		}
		c.Index = n
		c.Mesg = c.Mesg[:i]
	}
	if i := strings.Index(c.Field, "["); i >= 0 && strings.HasSuffix(c.Field, "]") {
		n, err := strconv.Atoi(c.Field[i+1 : len(c.Field)-1])
		if err != nil {
			return Change{}, fmt.Errorf("bad array index in %q", left)
		}
		c.ArrayIndex = &n
		c.Field = c.Field[:i]
	}
	return c, nil
}

func enumOptions(p *fit.Profile, typeName string) []string {
	vals, ok := p.Types[typeName]
	if !ok || len(vals) == 0 || len(vals) > 64 {
		return nil
	}
	type kv struct {
		v int64
		n string
	}
	var list []kv
	for v, n := range vals {
		list = append(list, kv{v, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v < list[j].v })
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.n
	}
	return out
}

// SchemaField describes a documented field for editors and assistants.
type SchemaField struct {
	Num     uint8    `json:"num"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Base    string   `json:"base"`
	Array   bool     `json:"array"`
	Units   string   `json:"units,omitempty"`
	Options []string `json:"options,omitempty"`
}

// Schema lists the documented fields of a message.
func (s *Service) Schema(mesg string) ([]SchemaField, error) {
	var mp *fit.MesgProfile
	if n, err := strconv.Atoi(mesg); err == nil {
		mp = s.profile.Messages[uint16(n)]
	} else if m, ok := s.profile.MessageByName(mesg); ok {
		mp = m
	}
	if mp == nil {
		return nil, fmt.Errorf("unknown message %q", mesg)
	}
	var out []SchemaField
	for _, f := range mp.Fields {
		out = append(out, SchemaField{Num: f.Num, Name: f.Name, Type: f.Type, Base: f.BaseType, Array: f.Array, Units: f.Units, Options: enumOptions(s.profile, f.Type)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Num < out[j].Num })
	return out, nil
}
