// Package apps owns the installed application registry and app operations.
package apps

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
