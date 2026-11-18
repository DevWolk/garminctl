package settings

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Resolve maps one explicit edit through the supplied profile and prior view.
func Resolve(profile *fit.Profile, change Change, view *View) (ResolvedChange, error) {
	r := resolverTask{profile: profile}
	return r.run(change, view)
}

type resolverTask struct{ profile *fit.Profile }

func loadSettingsTask(ctx context.Context, sess *watchsession.Session, profile *fit.Profile) (*View, error) {
	b, err := sess.ReadThrough(ctx, PathSettings, true)
	if err != nil {
		return nil, err
	}

	f, err := fit.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PathSettings, err)
	}

	return &View{Path: PathSettings, Messages: profile.Describe(f), Knowledge: DefaultKnowledge()}, nil
}

// Resolve maps a change onto message and field numbers using the profile
// and, when available, the current settings for the previous value.
func (s *resolverTask) run(c Change, view *View) (ResolvedChange, error) {
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

	if err := checkBaseRange(targetBase(view, rc, fp), raw); err != nil {
		return rc, fmt.Errorf("field %s.%s: %w", c.Mesg, c.Field, err)
	}

	rc.Raw = raw

	if view != nil {
		rc.Previous = previousValue(view, rc)
	}

	return rc, nil
}

// targetBase returns the base type name of the field the change targets:
// the definition in the current settings when the field is there, otherwise
// the profile's, otherwise "".
func targetBase(view *View, rc ResolvedChange, fp *fit.FieldProfile) string {
	if f := viewField(view, rc); f != nil {
		return f.Base
	}

	if fp != nil {
		return fp.BaseType
	}

	return ""
}

// checkBaseRange refuses an integer outside the range of an integer base
// type; float, string and unknown base types are not checked here.
func checkBaseRange(base string, raw int64) error {
	bt, ok := fit.BaseTypeByName(base)
	if !ok {
		return nil
	}

	if bt == fit.BaseFloat32 || bt == fit.BaseFloat64 || bt == fit.BaseString {
		return nil
	}

	return bt.CheckInteger(raw)
}

// resolveMesg sets rc.MesgNum from a profile name or a number and returns
// the message profile, nil for an undocumented number.
func (s *resolverTask) resolveMesg(rc *ResolvedChange) (*fit.MesgProfile, error) {
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

// viewField returns the field the change targets in the settings view.
func viewField(view *View, rc ResolvedChange) *fit.NamedField {
	if view == nil {
		return nil
	}

	m, ok := view.Message(strconv.Itoa(int(rc.MesgNum)), rc.Index)
	if !ok {
		return nil
	}

	f, ok := m.FieldByName(strconv.Itoa(int(rc.FieldNum)))
	if !ok {
		return nil
	}

	return f
}

// previousValue reads the field's current value from the settings view.
func previousValue(view *View, rc ResolvedChange) any {
	f := viewField(view, rc)
	if f == nil {
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
