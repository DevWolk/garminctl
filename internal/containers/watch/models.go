// Package watch provides watch summaries, file interpretation and workflows.
package watch

import "github.com/DevWolk/garminctl/internal/device"

// Summary describes the connected watch.
type Summary struct {
	Device      device.Info     `json:"device"`
	Model       string          `json:"model"`
	PartNumber  string          `json:"partNumber"`
	UnitID      uint64          `json:"unitId"`
	Software    string          `json:"software"`
	AppCount    int             `json:"appCount"`
	NewFiles    int             `json:"pendingNewFiles"`
	ProfileVers string          `json:"fitProfile"`
	Storage     *device.Storage `json:"storage,omitempty"`
}
