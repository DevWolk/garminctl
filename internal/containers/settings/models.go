package settings

import (
	"strconv"

	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Settings paths on the watch.
const (
	PathSettings = watchsession.PathSettings
	PathNewFiles = watchsession.PathNewFiles
)

// View is the decoded Settings.fit plus notes.
type View struct {
	Path      string             `json:"path"`
	Messages  []fit.NamedMessage `json:"messages"`
	Knowledge *Knowledge         `json:"knowledge"`
}

// Message returns the message with the given name or number and index.
func (v *View) Message(nameOrNum string, index int) (*fit.NamedMessage, bool) {
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

// ApplyResult reports what was written to the watch.
type ApplyResult struct {
	Path    string           `json:"path"`
	Bytes   int              `json:"bytes"`
	Changes []ResolvedChange `json:"changes"`
	Note    string           `json:"note"`
}

// Row is one field of the flattened view, with notes merged in.
type Row struct {
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
func (v *View) Rows() []Row {
	return v.rows(fit.MustProfile())
}

func (v *View) rows(p *fit.Profile) []Row {
	var rows []Row

	for _, m := range v.Messages {
		mesgName := m.Name
		if mn, ok := v.Knowledge.Messages[strconv.Itoa(int(m.Num))]; ok && mesgName == "" {
			mesgName = mn.Name
		}

		if mesgName == "" {
			mesgName = "mesg" + strconv.Itoa(int(m.Num))
		}

		for _, f := range m.Fields {
			r := Row{Mesg: mesgName, MesgNum: m.Num, Index: m.Index, Field: f.Name, FieldNum: f.Num, Type: f.Type, Units: f.Units, Known: f.Known, Value: f.Value, Raw: f.Raw}

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
