package fit

import (
	"fmt"
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
// values and plain numbers are accepted.
func (p *Profile) ParseValue(fp *FieldProfile, v any) (any, error) {
	if fp == nil {
		return v, nil
	}
	if s, ok := v.(string); ok {
		if _, isEnum := p.Types[fp.Type]; isEnum {
			if ev, ok := p.EnumValue(fp.Type, s); ok {
				return ev, nil
			}
		}
		if fp.BaseType == "string" {
			return s, nil
		}
		if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
			v = iv
		} else if fv, err := strconv.ParseFloat(s, 64); err == nil {
			v = fv
		} else if s == "true" {
			v = int64(1)
		} else if s == "false" {
			v = int64(0)
		} else {
			return nil, fmt.Errorf("%s: cannot parse %q", fp.Name, s)
		}
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
		return int64(scaled + 0.5), nil
	}
	return v, nil
}
