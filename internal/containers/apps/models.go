// Package apps owns the installed application registry and app operations.
package apps

import "github.com/DevWolk/garminctl/internal/ciq"

// App is an installed application listed in GARMIN/Apps/OUT.BIN.
type App struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	TypeName string `json:"typeName"`
	Name     string `json:"name"`
	Hidden   bool   `json:"hidden"` // field 4: hidden from activity, glance or app lists
	Version  int    `json:"version,omitempty"`
	FileName string `json:"fileName,omitempty"` // Connect IQ apps only, e.g. G5RG4154
	Size     int    `json:"size,omitempty"`
	NativeID int    `json:"nativeId,omitempty"` // built-in apps only
	Native   bool   `json:"native"`
}

// AppChange is one difference between the registry and the pending IN.BIN.
type AppChange struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TypeName string `json:"typeName"`
	Hidden   bool   `json:"hidden"` // the value that applies on unplug
}

// AppSettingItem is one setting with its label and current value.
type AppSettingItem struct {
	Key        string `json:"key"`
	Label      string `json:"label,omitempty"`
	Type       string `json:"type"` // select, toggle, text, number, or the raw Go type when no schema
	Value      any    `json:"value"`
	ValueLabel string `json:"valueLabel,omitempty"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
	Options    int    `json:"options,omitempty"` // number of dropdown options (see schema)
}

// AppSettings is the settings of one Connect IQ app.
type AppSettings struct {
	App          App              `json:"app"`
	Path         string           `json:"path"`
	Settings     ciq.Dict         `json:"-"`
	Values       map[string]any   `json:"values"`
	Order        []string         `json:"order"`
	Items        []AppSettingItem `json:"items"`
	SchemaSource string           `json:"schemaSource,omitempty"`
	SchemaError  string           `json:"schemaError,omitempty"`
	Hints        *AppHints        `json:"hints,omitempty"`
}
