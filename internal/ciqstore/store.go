package ciqstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The Connect IQ store website's own JSON API. It answers without a login.
const storeAPI = "https://apps.garmin.com/api/appsLibraryExternalServices/api/asw"

// StoreTypes maps the website's appType query values to labels; the
// numeric typeId in records uses the same order (1..5).
var StoreTypes = []struct{ Key, Label string }{
	{"watchface", "Watch faces"}, {"watch-app", "Apps"}, {"widget", "Widgets"}, {"datafield", "Data fields"}, {"audio-content-provider-app", "Music"},
}

var storeTypeByID = map[string]string{"1": "watchface", "2": "watch-app", "3": "widget", "4": "datafield", "5": "audio-content-provider-app"}

// StoreSorts are the accepted sortType values (empty = most relevant).
var StoreSorts = []struct{ Key, Label string }{
	{"mostPopular", "Most popular"}, {"highestRated", "Highest rated"}, {"mostRecent", "Newest"}, {"trending", "Trending"},
}

// StoreApp is a store record trimmed for display.
type StoreApp struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Developer       string   `json:"developer"`
	Rating          float64  `json:"rating"`
	ReviewCount     int      `json:"reviewCount"`
	Downloads       int      `json:"downloads"`
	Version         string   `json:"version"`
	InternalVersion int      `json:"internalVersion"`
	Price           string   `json:"price,omitempty"`
	TrialMode       bool     `json:"trialMode"`
	IconURL         string   `json:"iconUrl,omitempty"`
	StoreURL        string   `json:"storeUrl"`
	Updated         string   `json:"updated,omitempty"`
	Compatible      bool     `json:"compatible"`
	Description     string   `json:"description,omitempty"`
	WhatsNew        string   `json:"whatsNew,omitempty"`
	Permissions     []string `json:"permissions,omitempty"`
	SizeBytes       int      `json:"sizeBytes,omitempty"`
	HasSettings     *bool    `json:"hasSettings,omitempty"`
}

// StoreTypeInfo is a key/label pair for the UI.
type StoreTypeInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// StoreTypeList returns the app types for the UI.
func StoreTypeList() []StoreTypeInfo {
	out := make([]StoreTypeInfo, 0, len(StoreTypes))
	for _, t := range StoreTypes {
		out = append(out, StoreTypeInfo{t.Key, t.Label})
	}

	return out
}

// StoreSortList returns the sort orders for the UI.
func StoreSortList() []StoreTypeInfo {
	out := make([]StoreTypeInfo, 0, len(StoreSorts))
	for _, t := range StoreSorts {
		out = append(out, StoreTypeInfo{t.Key, t.Label})
	}

	return out
}

// StoreReview is one user review.
type StoreReview struct {
	Reviewer string `json:"reviewer"`
	Rating   int    `json:"rating"`
	Text     string `json:"text"`
	Date     string `json:"date,omitempty"`
	Version  string `json:"version,omitempty"`
}

// StorePage is a page of results.
type StorePage struct {
	Total int        `json:"total"`
	Apps  []StoreApp `json:"apps"`
}

type rawApp struct {
	ID               string `json:"id"`
	TypeID           string `json:"typeId"`
	AppLocalizations []struct {
		Locale      string `json:"locale"`
		Name        string `json:"name"`
		Description string `json:"description"`
		WhatsNew    string `json:"whatsNew"`
	} `json:"appLocalizations"`
	IconFileID            string   `json:"iconFileId"`
	LatestExternalVersion string   `json:"latestExternalVersion"`
	LatestInternalVersion int      `json:"latestInternalVersion"`
	DownloadCount         int      `json:"downloadCount"`
	ChangedDate           int64    `json:"changedDate"`
	AverageRating         float64  `json:"averageRating"`
	ReviewCount           int      `json:"reviewCount"`
	CompatibleParts       []string `json:"compatibleDevicePartNumbers"`
	HasTrialMode          bool     `json:"hasTrialMode"`
	Permissions           []string `json:"permissions"`
	Developer             struct {
		DeveloperDisplayName string `json:"developerDisplayName"`
	} `json:"developer"`
	Pricing *struct {
		SalePrice struct {
			FormattedPrice string `json:"formattedPrice"`
		} `json:"salePrice"`
	} `json:"pricing"`
	FileSizeInfo *struct {
		ByteCountByDeviceTypeID map[string]int `json:"byteCountByDeviceTypeId"`
	} `json:"fileSizeInfo"`
	SettingsAvailabilityInfo *struct {
		AvailabilityByDeviceTypeID map[string]bool `json:"availabilityByDeviceTypeId"`
	} `json:"settingsAvailabilityInfo"`
}

func (r *rawApp) toApp(partNumber, deviceTypeID string) StoreApp {
	a := StoreApp{
		ID: r.ID, Type: storeTypeByID[r.TypeID], Developer: r.Developer.DeveloperDisplayName, Rating: r.AverageRating, ReviewCount: r.ReviewCount, Downloads: r.DownloadCount,
		Version: r.LatestExternalVersion, InternalVersion: r.LatestInternalVersion, TrialMode: r.HasTrialMode, StoreURL: "https://apps.garmin.com/apps/" + r.ID, Permissions: r.Permissions,
	}
	if a.Type == "" {
		a.Type = "type" + r.TypeID
	}

	a.Name, a.Description, a.WhatsNew = r.localized()

	if r.IconFileID != "" {
		a.IconURL = fmt.Sprintf("https://services.garmin.com/appsLibraryBusinessServices_v0/rest/apps/%s/icon/%s", r.ID, r.IconFileID)
	}

	if r.Pricing != nil && r.Pricing.SalePrice.FormattedPrice != "" {
		a.Price = r.Pricing.SalePrice.FormattedPrice
	}

	if r.ChangedDate > 0 {
		a.Updated = time.UnixMilli(r.ChangedDate).UTC().Format(time.DateOnly)
	}

	a.Compatible = r.compatibleWith(partNumber)
	a.SizeBytes, a.HasSettings = r.forDeviceType(deviceTypeID)

	return a
}

// localized returns the English texts, or the first localisation on offer.
func (r *rawApp) localized() (name, description, whatsNew string) {
	loc := -1

	for i, l := range r.AppLocalizations {
		if l.Locale == "en-US" || l.Locale == "en" {
			loc = i
			break
		}
	}

	if loc < 0 && len(r.AppLocalizations) > 0 {
		loc = 0
	}

	if loc < 0 {
		return "", "", ""
	}

	l := r.AppLocalizations[loc]

	return l.Name, l.Description, l.WhatsNew
}

// compatibleWith reports whether the app lists the watch's part number; an
// unknown part number or an app without a list counts as compatible.
func (r *rawApp) compatibleWith(partNumber string) bool {
	if partNumber == "" || len(r.CompatibleParts) == 0 {
		return true
	}

	for _, p := range r.CompatibleParts {
		if strings.EqualFold(p, partNumber) {
			return true
		}
	}

	return false
}

// forDeviceType returns the binary size and settings availability the store
// records for one device type.
func (r *rawApp) forDeviceType(deviceTypeID string) (sizeBytes int, hasSettings *bool) {
	if deviceTypeID == "" {
		return 0, nil
	}

	if r.FileSizeInfo != nil {
		sizeBytes = r.FileSizeInfo.ByteCountByDeviceTypeID[deviceTypeID]
	}

	if r.SettingsAvailabilityInfo != nil {
		if v, ok := r.SettingsAvailabilityInfo.AvailabilityByDeviceTypeID[deviceTypeID]; ok {
			hasSettings = &v
		}
	}

	return sizeBytes, hasSettings
}

func storeGet(ctx context.Context, p string, q url.Values, out any) error {
	u := storeAPI + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 garminctl")
	req.Header.Set("Accept", "application/json")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Message string }

		_ = json.Unmarshal(b, &e)
		if e.Message != "" {
			return fmt.Errorf("store answered %s: %s", resp.Status, e.Message)
		}

		return fmt.Errorf("store answered %s", resp.Status)
	}

	return json.Unmarshal(b, out)
}

// DeviceTypeID resolves the store's numeric device id for a part number.
func DeviceTypeID(ctx context.Context, partNumber string) (string, error) {
	var list []struct {
		ID         string `json:"id"`
		PartNumber string `json:"partNumber"`
	}
	if err := storeGet(ctx, "/deviceTypes", nil, &list); err != nil {
		return "", err
	}

	for _, d := range list {
		if strings.EqualFold(d.PartNumber, partNumber) {
			return d.ID, nil
		}
	}

	return "", nil
}

// StoreQuery selects a page of the store.
type StoreQuery struct {
	Keywords     string
	Type         string // StoreTypes key or empty
	Sort         string // StoreSorts key or empty
	Page         int
	PageSize     int
	PartNumber   string
	DeviceTypeID string
}

// Browse lists or searches the store.
func Browse(ctx context.Context, q StoreQuery) (*StorePage, error) {
	if q.PageSize <= 0 {
		q.PageSize = 24
	}

	vals := url.Values{"startPageIndex": {strconv.Itoa(q.Page * q.PageSize)}, "pageSize": {strconv.Itoa(q.PageSize)}, "countryCode": {"US"}}
	page := &StorePage{Apps: []StoreApp{}}

	if strings.TrimSpace(q.Keywords) != "" {
		vals.Set("keywords", strings.TrimSpace(q.Keywords))
		vals.Set("locale", "en-US")

		var r struct {
			TotalCount int      `json:"totalCount"`
			Apps       []rawApp `json:"apps"`
		}
		if err := storeGet(ctx, "/apps/keywords", vals, &r); err != nil {
			return nil, err
		}

		page.Total = r.TotalCount
		for i := range r.Apps {
			a := r.Apps[i].toApp(q.PartNumber, q.DeviceTypeID)
			if q.Type != "" && a.Type != q.Type {
				continue
			}

			page.Apps = append(page.Apps, a)
		}

		return page, nil
	}

	if q.Type == "" {
		q.Type = "watchface"
	}

	vals.Set("appType", q.Type)

	if q.PartNumber != "" {
		vals.Set("partNumber", q.PartNumber)
	}

	if q.Sort != "" {
		vals.Set("sortType", q.Sort)
	}

	var list []rawApp
	if err := storeGet(ctx, "/apps", vals, &list); err != nil {
		return nil, err
	}

	for i := range list {
		page.Apps = append(page.Apps, list[i].toApp(q.PartNumber, q.DeviceTypeID))
	}

	page.Total = -1 // the listing endpoint does not report a total

	return page, nil
}

// Detail fetches one app with its description.
func Detail(ctx context.Context, appID, partNumber, deviceTypeID string) (*StoreApp, error) {
	var r rawApp
	if err := storeGet(ctx, "/apps/"+url.PathEscape(appID), url.Values{"countryCode": {"US"}}, &r); err != nil {
		return nil, err
	}

	a := r.toApp(partNumber, deviceTypeID)

	return &a, nil
}

// Reviews fetches the newest reviews of an app.
func Reviews(ctx context.Context, appID string, n int) ([]StoreReview, error) {
	if n <= 0 {
		n = 10
	}

	var list []struct {
		ReviewerFullName string `json:"reviewerFullName"`
		Rating           int    `json:"rating"`
		Text             string `json:"text"`
		CreatedDate      int64  `json:"createdDate"`
		AppVersion       string `json:"appVersionName"`
	}
	if err := storeGet(ctx, "/apps/"+url.PathEscape(appID)+"/reviews", url.Values{"pageSize": {strconv.Itoa(n)}, "startPageIndex": {"0"}}, &list); err != nil {
		return nil, err
	}

	out := make([]StoreReview, 0, len(list))
	for _, r := range list {
		rv := StoreReview{Reviewer: r.ReviewerFullName, Rating: r.Rating, Text: strings.TrimSpace(r.Text), Version: r.AppVersion}
		if r.CreatedDate > 0 {
			rv.Date = time.UnixMilli(r.CreatedDate).UTC().Format(time.DateOnly)
		}

		out = append(out, rv)
	}

	return out, nil
}
