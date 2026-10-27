package garmin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/fit"
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
	if err := validateChangeIndices(c); err != nil {
		return rc, err
	}

	mp, err := s.resolveMesg(&rc)
	if err != nil {
		return rc, err
	}

	fp, err := resolveField(&rc, mp)
	if err != nil {
		return rc, err
	}

	v, err := s.profile.ParseValue(fp, c.Value)
	if err != nil {
		return rc, err
	}

	raw, err := changeRaw(v)
	if err != nil {
		return rc, fmt.Errorf("field %s.%s: %w", c.Mesg, c.Field, err)
	}

	rc.Raw = raw

	if view != nil {
		rc.Previous = previousValue(view, rc)
	}

	return rc, nil
}

// resolveMesg sets rc.MesgNum from a profile name or a number and returns
// the message profile, nil for an undocumented number.
func (s *Service) resolveMesg(rc *ResolvedChange) (*fit.MesgProfile, error) {
	if n, err := strconv.Atoi(rc.Mesg); err == nil {
		if n < 0 || n > math.MaxUint16 {
			return nil, fmt.Errorf("message number %d is out of range", n)
		}

		rc.MesgNum = uint16(n)

		return s.profile.Messages[rc.MesgNum], nil
	}

	m, ok := s.profile.MessageByName(rc.Mesg)
	if !ok {
		return nil, fmt.Errorf("unknown message %q", rc.Mesg)
	}

	rc.MesgNum = m.Num

	return m, nil
}

// resolveField sets rc.FieldNum from a profile name or a number and returns
// the field profile, nil for an undocumented number.
func resolveField(rc *ResolvedChange, mp *fit.MesgProfile) (*fit.FieldProfile, error) {
	if n, err := strconv.Atoi(rc.Field); err == nil {
		if n < 0 || n > math.MaxUint8 {
			return nil, fmt.Errorf("field number %d is out of range", n)
		}

		rc.FieldNum = uint8(n)

		if mp == nil {
			return nil, nil // an undocumented message: the field has no profile either
		}

		return mp.Fields[rc.FieldNum], nil
	}

	if mp == nil {
		return nil, fmt.Errorf("message %s is undocumented; address its fields by number", rc.Mesg)
	}

	f, ok := mp.FieldByName(rc.Field)
	if !ok {
		return nil, fmt.Errorf("message %s has no field %q", rc.Mesg, rc.Field)
	}

	rc.FieldNum = f.Num

	return f, nil
}

// changeRaw turns a parsed value into the unscaled integer the change file
// carries in its 32-bit value slot.
func changeRaw(v any) (int64, error) {
	if _, ok := v.(string); ok {
		return 0, errors.New("string values cannot be sent as a settings change")
	}

	raw, err := toInt64(v)
	if err != nil {
		return 0, err
	}

	if raw < math.MinInt32 || raw > math.MaxUint32 {
		return 0, fmt.Errorf("value %d does not fit the 32-bit slot of a settings change", raw)
	}

	return raw, nil
}

// previousValue reads the field's current value from the settings view.
func previousValue(view *SettingsView, rc ResolvedChange) any {
	m, ok := view.Message(strconv.Itoa(int(rc.MesgNum)), rc.Index)
	if !ok {
		return nil
	}

	f, ok := m.FieldByName(strconv.Itoa(int(rc.FieldNum)))
	if !ok {
		return nil
	}

	if arr, isArr := f.Raw.([]any); isArr && rc.ArrayIndex != nil && *rc.ArrayIndex < len(arr) {
		return arr[*rc.ArrayIndex]
	}

	return f.Value
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
		if x > math.MaxInt64 {
			return 0, fmt.Errorf("value %d is too large", x)
		}

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
		if x != math.Trunc(x) || x < -0x1p63 || x >= 0x1p63 {
			return 0, fmt.Errorf("finite int64 value expected, got %v", x)
		}

		return int64(x), nil
	}

	return 0, fmt.Errorf("integer expected, got %T", v)
}

func validateChangeIndices(c Change) error {
	if c.Index < 0 || c.Index > math.MaxUint16 {
		return fmt.Errorf("message index %d does not fit the settings-change slot", c.Index)
	}

	if c.ArrayIndex != nil && (*c.ArrayIndex < 0 || *c.ArrayIndex > math.MaxUint8) {
		return fmt.Errorf("array index %d does not fit the settings-change slot", *c.ArrayIndex)
	}

	return nil
}

// BuildChangeFile produces a settings-change FIT file the watch applies
// from GARMIN/NewFiles. unitID and swVersion come from the device.
func BuildChangeFile(changes []ResolvedChange, unitID uint64, swVersion uint16, now time.Time) ([]byte, error) {
	b := fit.NewBuilder(21217, true)

	ts := fit.Timestamp(now)
	if err := b.Write(0, []fit.FieldDef{
		{Num: 0, Size: 1, BaseType: fit.BaseEnum},
		{Num: 1, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 2, BaseType: fit.BaseUint16},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
		{Num: 3, Size: 4, BaseType: fit.BaseUint32z},
		{Num: 5, Size: 2, BaseType: fit.BaseUint16},
	}, map[uint8]any{0: FileTypeSettingsChange, 1: 1, 2: 65534, 4: ts, 3: fitSerial(unitID), 5: 1}); err != nil {
		return nil, err
	}

	if err := b.Write(49, []fit.FieldDef{
		{Num: 1, Size: 1, BaseType: fit.BaseUint8},
		{Num: 0, Size: 2, BaseType: fit.BaseUint16},
	}, map[uint8]any{1: 0, 0: swVersion}); err != nil {
		return nil, err
	}

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
		if err := validateChangeIndices(c.Change); err != nil {
			return nil, err
		}

		if c.Raw < math.MinInt32 || c.Raw > math.MaxUint32 {
			return nil, fmt.Errorf("raw value %d does not fit the settings-change slot", c.Raw)
		}

		// The value slot is a uint32; Resolve keeps Raw within int32..uint32,
		// and a negative value travels as its two's complement.
		raw := uint32(c.Raw)

		vals := map[uint8]any{253: ts, 4: raw, 0: c.MesgNum, 2: c.FieldNum}
		if c.ArrayIndex != nil || c.Index > 0 {
			vals[1] = c.Index

			ai := 0
			if c.ArrayIndex != nil {
				ai = *c.ArrayIndex
			}

			vals[3] = ai
			if err := b.Write(MesgSettingsChange, indexed, vals); err != nil {
				return nil, err
			}
		} else {
			if err := b.Write(MesgSettingsChange, simple, vals); err != nil {
				return nil, err
			}
		}
	}

	return b.Bytes(), nil
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
		return nil, errors.New("no changes given")
	}

	view, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}

	dx, err := s.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	if dx.SoftwareVersion < 0 || dx.SoftwareVersion > math.MaxUint16 {
		return nil, fmt.Errorf("software version %d in GarminDevice.xml is out of range", dx.SoftwareVersion)
	}

	var resolved []ResolvedChange

	for _, c := range changes {
		rc, err := s.Resolve(c, view)
		if err != nil {
			return nil, err
		}

		resolved = append(resolved, rc)
	}

	data, err := BuildChangeFile(resolved, dx.UnitID, uint16(dx.SoftwareVersion), time.Now())
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%s/%s.FIT", PathNewFiles, newUUID())
	if err := s.WriteFile(ctx, name, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", name, err)
	}

	s.invalidate(PathSettings)

	note := "Disconnect the watch to apply the settings."

	var managed []string

	for _, rc := range resolved {
		if s.know.ManagedByConnect(rc.MesgNum, rc.FieldNum) {
			managed = append(managed, rc.Mesg+"."+rc.Field)
		}
	}

	if len(managed) > 0 {
		note += " Warning: Garmin Connect may overwrite " + strings.Join(managed, ", ") + " during sync. Set the same values in Garmin Connect to keep them."
	}

	return &ApplyResult{Path: name, Bytes: len(data), Changes: resolved, Note: note}, nil
}

// SettingsRow is one field of the flattened view, with notes merged in.
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
	// ManagedByConnect marks fields that Garmin Connect may overwrite during sync.
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
	left, value, ok := strings.Cut(spec, "=")
	if !ok {
		return Change{}, fmt.Errorf("expected mesg.field=value, got %q", spec)
	}

	mesg, field, ok := strings.Cut(left, ".")
	if !ok {
		return Change{}, fmt.Errorf("expected mesg.field, got %q", left)
	}

	c := Change{Mesg: mesg, Field: field, Value: value}
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

	if err := validateChangeIndices(c); err != nil {
		return Change{}, err
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
		if n >= 0 && n <= math.MaxUint16 {
			mp = s.profile.Messages[uint16(n)]
		}
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
