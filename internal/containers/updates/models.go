// Package updates owns watch firmware/data delivery, inventory and app version checks.
package updates

import (
	"net/http"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/apps"
)

// DefaultURL is Garmin's device software update service.
const DefaultURL = "https://omt.garmin.com/api/device-software/v1/updates"

// HTTPClient checks and downloads updates for the default Config; tests may
// replace it. Firmware downloads can take minutes.
var HTTPClient = &http.Client{Timeout: 10 * time.Minute}

// Config supplies this feature's update endpoint and shared network client.
type (
	Config struct {
		URL    string
		Client *http.Client
	}
	// SoftwareUpdate is one item the update service offers for the watch.
	SoftwareUpdate struct {
		PartNumber         string   `json:"partNumber"`
		ProductName        string   `json:"productName"`
		Version            string   `json:"version"`
		DataType           string   `json:"dataType"`
		Description        string   `json:"description"`
		Severity           string   `json:"severity"`
		IsPrimaryFirmware  bool     `json:"isPrimaryFirmware"`
		IsRestartRequired  bool     `json:"isRestartRequired"`
		IsProductInstalled bool     `json:"isProductInstalled"`
		SizeBytes          int64    `json:"sizeBytes"`
		DownloadURL        string   `json:"downloadUrl"`
		MD5                string   `json:"md5"`
		InstallPath        string   `json:"installPath"`
		FileName           string   `json:"fileName"`
		EulaURL            string   `json:"eulaUrl,omitempty"`
		ReleaseNotes       []string `json:"releaseNotes,omitempty"`
		ReleaseDate        string   `json:"releaseDate,omitempty"`
	}
)

// InstallResult reports an installed update.
type InstallResult struct {
	PartNumber string `json:"partNumber"`
	Path       string `json:"path"`
	Bytes      int    `json:"bytes"`
	Restart    bool   `json:"restartRequired"`
	Note       string `json:"note"`
}

// SoftwarePart is one installed software component reported in device.fit.
type SoftwarePart struct {
	PartNumber string  `json:"partNumber"`
	Version    float64 `json:"version"`
}

// AppUpdate compares an installed Connect IQ app with the store.
type AppUpdate struct {
	App              apps.App `json:"app"`
	InstalledVersion int      `json:"installedVersion"`
	LatestVersion    int      `json:"latestVersion"`
	LatestName       string   `json:"latestName,omitempty"`
	StoreURL         string   `json:"storeUrl"`
	UpdateAvailable  bool     `json:"updateAvailable"`
	Error            string   `json:"error,omitempty"`
}
