package fit

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"sync"
)

//go:embed profile.json
var profileJSON []byte

// Profile is the public FIT profile: message and field names, types and
// scaling, generated from Garmin's FIT SDK.
type Profile struct {
	Version  string
	Messages map[uint16]*MesgProfile
	Types    map[string]map[int64]string
	byName   map[string]*MesgProfile
}

// MesgProfile describes one documented message.
type MesgProfile struct {
	Num    uint16
	Name   string
	Fields map[uint8]*FieldProfile
	byName map[string]*FieldProfile
}

// FieldProfile describes one documented field.
type FieldProfile struct {
	Num      uint8
	Name     string
	Type     string // profile type name, e.g. "backlightMode" or "uint16"
	BaseType string
	Array    bool
	Scale    float64
	Offset   float64
	Units    string
}

// FieldByName finds a field by its profile name.
func (m *MesgProfile) FieldByName(name string) (*FieldProfile, bool) {
	f, ok := m.byName[name]
	return f, ok
}

// MessageByName finds a message by its profile name.
func (p *Profile) MessageByName(name string) (*MesgProfile, bool) {
	m, ok := p.byName[name]
	return m, ok
}

// EnumName returns the symbolic name for a value of a profile type.
func (p *Profile) EnumName(typeName string, v int64) (string, bool) {
	vals, ok := p.Types[typeName]
	if !ok {
		return "", false
	}
	n, ok := vals[v]
	return n, ok
}

// EnumValue returns the value for a symbolic name of a profile type.
func (p *Profile) EnumValue(typeName, name string) (int64, bool) {
	vals, ok := p.Types[typeName]
	if !ok {
		return 0, false
	}
	for v, n := range vals {
		if n == name {
			return v, true
		}
	}
	return 0, false
}

var (
	profileOnce sync.Once
	profileVal  *Profile
	profileErr  error
)

// DefaultProfile returns the embedded FIT profile.
func DefaultProfile() (*Profile, error) {
	profileOnce.Do(func() {
		profileVal, profileErr = parseProfile(profileJSON)
	})
	return profileVal, profileErr
}

// MustProfile returns the embedded profile or panics.
func MustProfile() *Profile {
	p, err := DefaultProfile()
	if err != nil {
		panic(err)
	}
	return p
}

type jsonProfile struct {
	Version struct {
		Major, Minor, Patch int
	} `json:"version"`
	Messages map[string]struct {
		Num    int `json:"num"`
		Name   string
		Fields map[string]struct {
			Num      int    `json:"num"`
			Name     string `json:"name"`
			Type     string `json:"type"`
			BaseType string `json:"baseType"`
			Array    bool   `json:"array"`
			Scale    any    `json:"scale"`
			Offset   any    `json:"offset"`
			Units    any    `json:"units"`
		} `json:"fields"`
	} `json:"messages"`
	Types map[string]map[string]any `json:"types"`
}

func parseProfile(data []byte) (*Profile, error) {
	var jp jsonProfile
	if err := json.Unmarshal(data, &jp); err != nil {
		return nil, err
	}
	p := &Profile{
		Version:  strconv.Itoa(jp.Version.Major) + "." + strconv.Itoa(jp.Version.Minor) + "." + strconv.Itoa(jp.Version.Patch),
		Messages: map[uint16]*MesgProfile{},
		Types:    map[string]map[int64]string{},
		byName:   map[string]*MesgProfile{},
	}
	for _, jm := range jp.Messages {
		m := &MesgProfile{Num: uint16(jm.Num), Name: jm.Name, Fields: map[uint8]*FieldProfile{}, byName: map[string]*FieldProfile{}}
		for _, jf := range jm.Fields {
			f := &FieldProfile{
				Num: uint8(jf.Num), Name: jf.Name, Type: jf.Type, BaseType: jf.BaseType, Array: jf.Array,
				Scale: numberOr(jf.Scale, 1), Offset: numberOr(jf.Offset, 0), Units: stringOr(jf.Units),
			}
			m.Fields[f.Num] = f
			m.byName[f.Name] = f
		}
		p.Messages[m.Num] = m
		p.byName[m.Name] = m
	}
	for tn, vals := range jp.Types {
		tm := map[int64]string{}
		for k, v := range vals {
			iv, err := strconv.ParseInt(k, 10, 64)
			if err != nil {
				continue
			}
			if s, ok := v.(string); ok {
				tm[iv] = s
			}
		}
		p.Types[tn] = tm
	}
	return p, nil
}

func numberOr(v any, def float64) float64 {
	switch x := v.(type) {
	case float64:
		if x == 0 && def == 1 {
			return 1
		}
		return x
	case []any:
		if len(x) > 0 {
			if f, ok := x[0].(float64); ok && f != 0 {
				return f
			}
		}
	}
	return def
}

func stringOr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		if len(x) > 0 {
			if s, ok := x[0].(string); ok {
				return s
			}
		}
	}
	return ""
}
