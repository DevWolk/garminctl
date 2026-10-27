package garmin

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Garmin's update manifest publishes MD5 checksums; integrity check only, not security
	"crypto/rand"
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

// ensureDir creates every missing segment of dir on the watch.
func (s *Service) ensureDir(ctx context.Context, dir string) error {
	cur := ""

	for seg := range strings.SplitSeq(dir, "/") {
		if seg == "" {
			continue
		}

		if cur == "" {
			cur = seg
		} else {
			cur += "/" + seg
		}

		if _, err := s.Stat(ctx, cur); err == nil {
			continue
		} else if !errors.Is(err, device.ErrNotFound) {
			return fmt.Errorf("stat %s: %w", cur, err)
		}

		if err := s.Mkdir(ctx, cur); err != nil {
			return fmt.Errorf("mkdir %s: %w", cur, err)
		}
	}

	return nil
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

// InstallPRG writes a Connect IQ binary to the app folder.
// The watch registers it after the USB session ends.
func (s *Service) InstallPRG(ctx context.Context, data []byte) (string, error) {
	if len(data) < 16 || data[0] != 0 || data[1] != 0 || data[2] != 0 {
		return "", errors.New("this does not look like a Connect IQ .PRG file")
	}

	var b [4]byte

	_, _ = rand.Read(b[:])
	name := strings.ToUpper(hex.EncodeToString(b[:])) + ".PRG"

	p, err := s.WriteAppFile(ctx, name, data)
	if err != nil {
		return "", err
	}

	s.invalidate(PathAppsConfig)

	return p, nil
}

// AppFolder stores Connect IQ binaries and may be hidden from ordinary MTP
// listings; see WriteAppFile.
const AppFolder = "GARMIN/APPS/MEDIA"

// WriteAppFile puts a Connect IQ binary (or STM store message) into the app
// folder and returns the watch path. When the folder is hidden from the
// ordinary listing it goes through the device's raw PTP path; the watch
// registers the app when the USB session ends.
func (s *Service) WriteAppFile(ctx context.Context, name string, data []byte) (string, error) {
	target := AppFolder + "/" + name
	if _, err := s.Stat(ctx, AppFolder); err == nil {
		if err := s.WriteFile(ctx, target, data); err != nil {
			return "", fmt.Errorf("write %s: %w", target, err)
		}

		return target, nil
	}

	if err := s.WriteHidden(ctx, AppFolder, name, data); errors.Is(err, ErrCapabilityUnavailable) {
		return "", errors.New("this watch does not expose its app folder (GARMIN/APPS/MEDIA) over USB, so apps can only be installed through Garmin Connect on the phone")
	} else if err != nil {
		return "", err
	}

	return target, nil
}

// RemoveApp deletes a Connect IQ app's binary, settings and data files. The
// watch drops the app from its registry when the USB session ends. ref is the
// app's file id, name or store id; built-in apps and the Connect IQ Store app
// are refused.
func (s *Service) RemoveApp(ctx context.Context, ref string) ([]string, error) {
	apps, err := s.Apps(ctx)
	if err != nil {
		return nil, err
	}

	ref = strings.TrimSuffix(strings.TrimSuffix(ref, ".PRG"), ".prg")

	app := installedApp(apps, ref)
	if app == nil {
		return nil, fmt.Errorf("%q is not an installed Connect IQ app", ref)
	}

	if app.Native {
		return nil, fmt.Errorf("%s is built into the watch and cannot be removed", app.Name)
	}

	if strings.EqualFold(app.FileName, "Toystore") {
		return nil, errors.New("the Connect IQ Store app cannot be removed")
	}

	var removed []string

	binary, err := s.removeAppBinary(ctx, app.FileName+".PRG")
	if err != nil {
		return removed, err
	}

	if binary != "" {
		removed = append(removed, binary)
	}

	removed, err = s.removeAppSidecars(ctx, app.FileName, removed)
	if err != nil {
		return removed, err
	}

	s.invalidate(PathAppsConfig)

	return removed, nil
}

// installedApp finds a store app by file id, name or store id.
func installedApp(apps []App, ref string) *App {
	for i := range apps {
		a := &apps[i]
		if a.FileName != "" && matchesApp(*a, ref) {
			return a
		}
	}

	return nil
}

// removeAppBinary deletes the .PRG from the visible or hidden app folder and
// returns the deleted path, or "" when it was not there.
func (s *Service) removeAppBinary(ctx context.Context, prg string) (string, error) {
	target := AppFolder + "/" + prg

	if _, err := s.Stat(ctx, target); err == nil {
		if err := s.DeleteFile(ctx, target); err != nil {
			return "", err
		}

		return target, nil
	}

	switch err := s.DeleteHidden(ctx, AppFolder, prg); {
	case errors.Is(err, ErrCapabilityUnavailable):
		return "", errors.New("this watch does not expose its app folder over USB from here")
	case err == nil:
		return target, nil
	case errors.Is(err, device.ErrNotFound):
		return "", nil
	default:
		return "", err
	}
}

// removeAppSidecars deletes the settings and data files of an app.
func (s *Service) removeAppSidecars(ctx context.Context, fileName string, removed []string) ([]string, error) {
	for _, p := range []string{"GARMIN/Apps/SETTINGS/" + fileName + ".SET", "GARMIN/Apps/DATA/" + fileName + ".DAT", "GARMIN/Apps/DATA/" + fileName + ".IDX", "GARMIN/Apps/DATA/" + fileName + ".IMT"} {
		if _, err := s.Stat(ctx, p); err != nil {
			continue
		}

		if err := s.DeleteFile(ctx, p); err != nil {
			return removed, err
		}

		removed = append(removed, p)
	}

	return removed, nil
}

// AppFiles lists the app folder, hidden or not.
func (s *Service) AppFiles(ctx context.Context) ([]device.Entry, error) {
	if entries, err := s.List(ctx, AppFolder); err == nil {
		return entries, nil
	}

	entries, err := s.ListHidden(ctx, AppFolder)
	if errors.Is(err, ErrCapabilityUnavailable) {
		return nil, device.ErrNotFound
	}

	return entries, err
}
