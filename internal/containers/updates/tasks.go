package updates

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
	"sort"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

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

func checkSoftwareUpdates(ctx context.Context, sess *watchsession.Session, config Config) ([]SoftwareUpdate, error) {
	xmlBytes, err := sess.ReadThrough(ctx, watchsession.PathDeviceXML, true)
	if err != nil {
		return nil, err
	}

	body, _ := json.Marshal(map[string]any{"GarminDeviceXml": string(xmlBytes)})

	url := config.URL

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "garminctl/0.1")

	resp, err := config.Client.Do(req)
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
func downloadUpdate(ctx context.Context, client *http.Client, u *SoftwareUpdate) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.DownloadURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
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
