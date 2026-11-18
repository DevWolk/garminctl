package fit

import (
	"fmt"
	"math"
	"strconv"
)

// NamedField is a field decoded through the profile.
type NamedField struct {
	Num   uint8  `json:"num"`
	Name  string `json:"name,omitempty"`
	Type  string `json:"type,omitempty"`
	Base  string `json:"base"`
	Units string `json:"units,omitempty"`
	Known bool   `json:"known"`
	// Raw is the unscaled numeric value (or string, or []any for arrays).
	Raw any `json:"raw"`
	// Value is Raw with scale/offset applied and enums named, when known.
	Value any `json:"value"`
}

// NamedMessage is a data record decoded through the profile.
type NamedMessage struct {
	Num    uint16       `json:"num"`
	Name   string       `json:"name,omitempty"`
	Known  bool         `json:"known"`
	Index  int          `json:"index"` // position among messages with the same Num
	Fields []NamedField `json:"fields"`
}

// FieldByName returns the field with the given profile name or numeric
// name ("12").
func (m *NamedMessage) FieldByName(name string) (*NamedField, bool) {
	for i := range m.Fields {
		if m.Fields[i].Name == name || strconv.Itoa(int(m.Fields[i].Num)) == name {
			return &m.Fields[i], true
		}
	}

	return nil, false
}

// Describe decodes every data record of f with the profile.
func (p *Profile) Describe(f *File) []NamedMessage {
	counts := map[uint16]int{}

	var out []NamedMessage

	for _, m := range f.Messages() {
		nm := p.DescribeMessage(m)
		nm.Index = counts[m.Def.GlobalNum]
		counts[m.Def.GlobalNum]++

		out = append(out, nm)
	}

	return out
}

// DescribeMessage decodes one data record with the profile.
func (p *Profile) DescribeMessage(m *Message) NamedMessage {
	nm := NamedMessage{Num: m.Def.GlobalNum}

	mp, ok := p.Messages[m.Def.GlobalNum]
	if ok {
		nm.Name = mp.Name
		nm.Known = true
	}

	for i := range m.Fields {
		fld := &m.Fields[i]
		nf := NamedField{Num: fld.Def.Num, Base: fld.Def.BaseType.String()}
		raw := fld.Value(m.Def.BigEndian)
		nf.Raw = raw
		nf.Value = raw

		if ok {
			if fp, ok := mp.Fields[fld.Def.Num]; ok {
				nf.Name = fp.Name
				nf.Type = fp.Type
				nf.Units = fp.Units
				nf.Known = true
				nf.Value = p.applyProfile(fp, raw)
			}
		}

		nm.Fields = append(nm.Fields, nf)
	}

	return nm
}

func (p *Profile) applyProfile(fp *FieldProfile, raw any) any {
	switch v := raw.(type) {
	case nil, string, []byte:
		return raw
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = p.applyProfile(fp, e)
		}

		return out
	}

	if _, isEnum := p.Types[fp.Type]; isEnum {
		if iv, err := toInt(raw); err == nil {
			if name, ok := p.EnumName(fp.Type, iv); ok {
				return name
			}
		}

		return raw
	}

	if fp.Scale != 1 || fp.Offset != 0 {
		fv, err := toFloat(raw)
		if err != nil {
			return raw
		}

		return fv/fp.Scale - fp.Offset
	}

	return raw
}

// ParseValue converts a user supplied value (string, number, bool) for the
// field into the raw integer or float the field stores. Enum names, scaled
// values and plain numbers are accepted. A nil fp is a field without a
// profile entry: a textual number or true/false is parsed, other values pass
// through unchanged. Scaled integer fields round half away from zero.
func (p *Profile) ParseValue(fp *FieldProfile, v any) (any, error) {
	if fp == nil {
		s, ok := v.(string)
		if !ok {
			return v, nil
		}

		parsed, _, err := p.parseString(&FieldProfile{Name: "value", Scale: 1}, s)

		return parsed, err
	}

	if s, ok := v.(string); ok {
		parsed, final, err := p.parseString(fp, s)
		if err != nil || final {
			return parsed, err
		}

		v = parsed
	}

	if fp.Scale != 1 || fp.Offset != 0 {
		fv, err := toFloat(v)
		if err != nil {
			return nil, err
		}

		scaled := (fv + fp.Offset) * fp.Scale
		if fp.BaseType == "float32" || fp.BaseType == "float64" {
			return scaled, nil
		}

		rounded := math.Round(scaled)
		if math.IsNaN(rounded) || rounded < -0x1p63 || rounded >= 0x1p63 {
			return nil, fmt.Errorf("%s: scaled value %v is outside int64 range", fp.Name, scaled)
		}

		return int64(rounded), nil
	}

	return v, nil
}

// parseString reads a textual value: an enum name or a string field value is
// final; a number or true/false still has the field's scale to apply.
func (p *Profile) parseString(fp *FieldProfile, s string) (parsed any, final bool, err error) {
	if _, isEnum := p.Types[fp.Type]; isEnum {
		if ev, ok := p.EnumValue(fp.Type, s); ok {
			return ev, true, nil
		}
	}

	if fp.BaseType == "string" {
		return s, true, nil
	}

	switch s {
	case "true":
		return int64(1), false, nil
	case "false":
		return int64(0), false, nil
	}

	if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
		return iv, false, nil
	}

	if fv, err := strconv.ParseFloat(s, 64); err == nil {
		return fv, false, nil
	}

	return nil, false, fmt.Errorf("%s: cannot parse %q", fp.Name, s)
}
