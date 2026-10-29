package garmin

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Garmin's update manifest publishes MD5 checksums; integrity check only, not security
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
)

// DefaultUpdatesURL is Garmin's device software update service.
// Requests contain the device XML and require no account session.
const DefaultUpdatesURL = "https://omt.garmin.com/api/device-software/v1/updates"

// HTTPClient is used for update checks and downloads.
var HTTPClient = &http.Client{Timeout: 10 * time.Minute}

// SoftwareUpdate is one item the update service offers for the watch.
type SoftwareUpdate struct {
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

type omtResponse struct {
	Updates []struct {
		PartNumber        string `json:"partNumber"`
		ProductName       string `json:"productName"`
		Version           string `json:"version"`
		DisplayVersion    string `json:"displayVersion"`
		IsPrimaryFirmware bool   `json:"isPrimaryFirmware"`
		DataType          string `json:"dataType"`
		Description       string `json:"description"`
		Severity          string `json:"severity"`
		ReleaseNotes      []struct {
			Version   string   `json:"version"`
			ChangeLog []string `json:"changeLog"`
		} `json:"releaseNotes"`
		EulaURL     string `json:"eulaUrl"`
		Deliverable struct {
			DownloadURL string `json:"downloadUrl"`
			MD5         string `json:"md5"`
			SizeInBytes int64  `json:"sizeInBytes"`
		} `json:"deliverable"`
		Installation struct {
			IsProductInstalled bool   `json:"isProductInstalled"`
			Path               string `json:"path"`
			FileName           string `json:"fileName"`
			IsRestartRequired  bool   `json:"isRestartRequired"`
		} `json:"installation"`
		ReleaseDate string `json:"releaseDate"`
	} `json:"updates"`
}

// CheckSoftwareUpdates asks Garmin which firmware and data updates apply to
// the connected watch.
func (s *Service) CheckSoftwareUpdates(ctx context.Context) ([]SoftwareUpdate, error) {
	xmlBytes, err := s.read(ctx, PathDeviceXML, true)
	if err != nil {
		return nil, err
	}

	body, _ := json.Marshal(map[string]any{"GarminDeviceXml": string(xmlBytes)})

	url := s.UpdatesURL // set by New; tests point it at their own server

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "garminctl/0.1")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update service answered %s", resp.Status)
	}

	var r omtResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}

	out := make([]SoftwareUpdate, 0, len(r.Updates))
	for _, u := range r.Updates {
		su := SoftwareUpdate{
			PartNumber: u.PartNumber, ProductName: u.ProductName, Version: firstNonEmpty(u.DisplayVersion, u.Version), DataType: u.DataType,
			Description: u.Description, Severity: u.Severity, IsPrimaryFirmware: u.IsPrimaryFirmware,
			IsRestartRequired: u.Installation.IsRestartRequired, IsProductInstalled: u.Installation.IsProductInstalled,
			SizeBytes: u.Deliverable.SizeInBytes, DownloadURL: u.Deliverable.DownloadURL, MD5: u.Deliverable.MD5,
			InstallPath: u.Installation.Path, FileName: u.Installation.FileName, EulaURL: u.EulaURL, ReleaseDate: u.ReleaseDate,
		}
		for _, rn := range u.ReleaseNotes {
			for _, line := range rn.ChangeLog {
				su.ReleaseNotes = append(su.ReleaseNotes, strings.TrimSpace(line))
			}
		}

		out = append(out, su)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].IsPrimaryFirmware && !out[j].IsPrimaryFirmware })

	return out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// InstallResult reports an installed update.
type InstallResult struct {
	PartNumber string `json:"partNumber"`
	Path       string `json:"path"`
	Bytes      int    `json:"bytes"`
	Restart    bool   `json:"restartRequired"`
	Note       string `json:"note"`
}

// InstallSoftwareUpdate downloads one update, verifies its MD5 and writes it
// where Garmin says the watch expects it. The watch installs it when the USB
// session ends.
func (s *Service) InstallSoftwareUpdate(ctx context.Context, partNumber string) (*InstallResult, error) {
	updates, err := s.CheckSoftwareUpdates(ctx)
	if err != nil {
		return nil, err
	}

	u, err := findUpdate(updates, partNumber)
	if err != nil {
		return nil, err
	}

	data, err := downloadUpdate(ctx, u)
	if err != nil {
		return nil, err
	}

	dir := strings.Trim(strings.ReplaceAll(u.InstallPath, "\\", "/"), "/")
	if err := s.ensureDir(ctx, dir); err != nil {
		return nil, err
	}

	target := path.Join(dir, u.FileName)
	if err := s.WriteFile(ctx, target, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", target, err)
	}

	s.invalidate(PathDeviceXML)

	restart := u.IsRestartRequired || u.IsPrimaryFirmware

	note := "Written to the watch. It installs the update when the USB session ends: unplug the watch."
	if restart {
		note += " The watch restarts to apply it; keep it charged and do not interrupt the restart."
	}

	return &InstallResult{PartNumber: u.PartNumber, Path: target, Bytes: len(data), Restart: restart, Note: note}, nil
}

// findUpdate picks the offered update with the given part number and checks
// that Garmin told us where it goes.
func findUpdate(updates []SoftwareUpdate, partNumber string) (*SoftwareUpdate, error) {
	var u *SoftwareUpdate

	for i := range updates {
		if updates[i].PartNumber == partNumber {
			u = &updates[i]
		}
	}

	if u == nil {
		return nil, fmt.Errorf("no update with part number %s is offered for this watch", partNumber)
	}

	if u.DownloadURL == "" || u.InstallPath == "" || u.FileName == "" {
		return nil, fmt.Errorf("%s: Garmin did not provide a download or install location", u.ProductName)
	}

	return u, nil
}

// downloadUpdate fetches the deliverable and checks its size and checksum.
func downloadUpdate(ctx context.Context, u *SoftwareUpdate) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.DownloadURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download answered %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	if u.SizeBytes > 0 && int64(len(data)) != u.SizeBytes {
		return nil, fmt.Errorf("download size %d does not match the expected %d bytes", len(data), u.SizeBytes)
	}

	if u.MD5 != "" {
		sum := md5.Sum(data) //nolint:gosec // Garmin's update manifest publishes MD5 checksums; integrity check only, not security
		if !strings.EqualFold(hex.EncodeToString(sum[:]), u.MD5) {
			return nil, errors.New("download checksum mismatch; nothing was written to the watch")
		}
	}

	return data, nil
}

func (s *Service) ensureDir(ctx context.Context, dir string) error {
	return s.session.EnsureDir(ctx, dir)
}

// SoftwarePart is one installed software component reported in device.fit.
type SoftwarePart struct {
	PartNumber string  `json:"partNumber"`
	Version    float64 `json:"version"`
}

// SoftwareInventory lists the software components the watch reports.
func (s *Service) SoftwareInventory(ctx context.Context) ([]SoftwarePart, error) {
	b, err := s.read(ctx, PathDeviceFIT, true)
	if err != nil {
		return nil, err
	}

	f, err := fit.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PathDeviceFIT, err)
	}

	seen := map[string]bool{}

	var out []SoftwarePart

	for _, m := range f.MessagesByNum(35) { // software
		pn, _ := m.Value(5).(string)

		ver := m.Value(3)
		if pn == "" || ver == nil {
			continue
		}

		fv, _ := ver.(uint64)

		key := fmt.Sprintf("%s@%d", pn, fv)
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, SoftwarePart{PartNumber: pn, Version: float64(fv) / 100})
	}

	return out, nil
}

// AppUpdate compares an installed Connect IQ app with the store.
type AppUpdate struct {
	App              App    `json:"app"`
	InstalledVersion int    `json:"installedVersion"`
	LatestVersion    int    `json:"latestVersion"`
	LatestName       string `json:"latestName,omitempty"`
	StoreURL         string `json:"storeUrl"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	Error            string `json:"error,omitempty"`
}

// CheckAppUpdates compares every store-installed app with the public store.
func (s *Service) CheckAppUpdates(ctx context.Context) ([]AppUpdate, error) {
	apps, err := s.Apps(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}

	var out []AppUpdate

	for _, a := range apps {
		if a.FileName == "" || a.ID == "" || seen[a.ID] {
			continue
		}

		seen[a.ID] = true
		au := AppUpdate{App: a, InstalledVersion: a.Version, StoreURL: "https://apps.garmin.com/apps/" + a.ID}

		info, err := ciqstore.AppInfo(ctx, a.ID)
		if err != nil {
			au.Error = err.Error()
		} else {
			au.LatestVersion, au.LatestName = info.LatestInternalVersion, info.LatestExternalVersion
			au.UpdateAvailable = info.LatestInternalVersion > a.Version
		}

		out = append(out, au)
	}

	return out, nil
}

// AppFolder preserves the app-owned binary folder path during migration.
const AppFolder = apps.AppFolder

// InstallPRG delegates validation/staging and shares this Session's feature cache.
func (s *Service) InstallPRG(ctx context.Context, data []byte) (string, error) {
	return apps.NewInstallPRGAction(&s.schemas).Run(ctx, s.session, data)
}

// WriteAppFile delegates named queue/binary delivery through the same Session.
func (s *Service) WriteAppFile(ctx context.Context, name string, data []byte) (string, error) {
	return (apps.WriteFileAction{}).Run(ctx, s.session, name, data)
}

// RemoveApp delegates app-file removal with the existing shared schema cache.
func (s *Service) RemoveApp(ctx context.Context, ref string) ([]string, error) {
	return apps.NewRemoveAction(&s.schemas).Run(ctx, s.session, ref)
}

// AppFiles delegates listing through the visible/hidden capability policy.
func (s *Service) AppFiles(ctx context.Context) ([]device.Entry, error) {
	return (apps.FilesAction{}).Run(ctx, s.session)
}
