// Package ciqstore reads Connect IQ store metadata and settings forms.
// Form controls supply setting labels, options and stored values.
package ciqstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/DevWolk/garminctl/internal/ship/paths"
)

// Option is one dropdown choice.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Setting describes one control of the settings form.
type Setting struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Type      string   `json:"type"` // select, toggle, text, number
	Default   string   `json:"default,omitempty"`
	Options   []Option `json:"options,omitempty"`
	Min       string   `json:"min,omitempty"`
	Max       string   `json:"max,omitempty"`
	Step      string   `json:"step,omitempty"`
	MaxLength int      `json:"maxLength,omitempty"`
	ReadOnly  bool     `json:"readOnly,omitempty"`
	Required  bool     `json:"required,omitempty"`
}

// OptionLabel returns the label of a value, or "".
func (s *Setting) OptionLabel(value string) string {
	for _, o := range s.Options {
		if o.Value == value {
			return o.Label
		}
	}

	return ""
}

// OptionValue resolves a user supplied label or value to an option value.
// Matching is case-insensitive; a unique prefix or substring also matches.
func (s *Setting) OptionValue(input string) (string, error) {
	in := strings.TrimSpace(input)
	for _, o := range s.Options {
		if o.Value == in {
			return o.Value, nil
		}
	}

	lower := strings.ToLower(in)

	var exact, partial []Option

	for _, o := range s.Options {
		l := strings.ToLower(o.Label)
		if l == lower {
			exact = append(exact, o)
		} else if strings.Contains(l, lower) {
			partial = append(partial, o)
		}
	}

	if len(exact) == 1 {
		return exact[0].Value, nil
	}

	if len(exact) == 0 && len(partial) == 1 {
		return partial[0].Value, nil
	}

	cands := slices.Concat(exact, partial)
	if len(cands) > 1 {
		var names []string
		for _, o := range cands {
			names = append(names, fmt.Sprintf("%q (%s)", o.Label, o.Value))
		}

		return "", fmt.Errorf("%s: %q matches several options: %s", s.Key, input, strings.Join(names, ", "))
	}

	return "", fmt.Errorf("%s: no option matches %q", s.Key, input)
}

// Schema is the parsed settings form.
type Schema struct {
	AppID      string    `json:"appId"`
	Version    int       `json:"version"`
	PartNumber string    `json:"partNumber"`
	Source     string    `json:"source"`
	FetchedAt  string    `json:"fetchedAt"`
	Settings   []Setting `json:"settings"`
	byKey      map[string]*Setting
}

// Get returns the setting with the given key. Schemas from Parse and Load
// are indexed and safe for concurrent reads; a literal Schema is scanned.
func (s *Schema) Get(key string) (*Setting, bool) {
	if s.byKey != nil {
		st, ok := s.byKey[key]
		return st, ok
	}

	for i := range s.Settings {
		if s.Settings[i].Key == key {
			return &s.Settings[i], true
		}
	}

	return nil, false
}

// index builds the key lookup; call it once the settings are final.
func (s *Schema) index() *Schema {
	s.byKey = make(map[string]*Setting, len(s.Settings))
	for i := range s.Settings {
		s.byKey[s.Settings[i].Key] = &s.Settings[i]
	}

	return s
}

// reID restricts app ids and device part numbers used in URLs and cache names.
var reID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

func checkIDs(appID, partNumber string) error {
	if err := checkAppID(appID); err != nil {
		return err
	}

	return checkPartNumber(partNumber)
}

func checkAppID(appID string) error {
	if !reID.MatchString(appID) {
		return fmt.Errorf("app id %q: only letters, digits and dashes are allowed", appID)
	}

	return nil
}

func checkPartNumber(partNumber string) error {
	if !reID.MatchString(partNumber) {
		return fmt.Errorf("part number %q: only letters, digits and dashes are allowed", partNumber)
	}

	return nil
}

// PageURL is the public settings page for an app version on a device.
// Callers validate appID and partNumber with checkIDs first.
func PageURL(appID string, version int, partNumber string) string {
	return fmt.Sprintf("https://apps.garmin.com/en-US/appSettings2/%s/versions/%d/devices/%s/edit?singlePage=true", appID, version, partNumber)
}

// HTTPClient is used for fetching; tests may replace it.
var HTTPClient = &http.Client{Timeout: 30 * time.Second}

// Fetch downloads and parses the settings form.
func Fetch(ctx context.Context, appID string, version int, partNumber string) (*Schema, error) {
	if err := checkIDs(appID, partNumber); err != nil {
		return nil, err
	}

	url := PageURL(appID, version, partNumber)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "garminctl/0.1 (+https://github.com)")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}

	schema, err := Parse(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	schema.AppID, schema.Version, schema.PartNumber = appID, version, partNumber
	schema.Source = url
	schema.FetchedAt = time.Now().UTC().Format(time.RFC3339)

	return schema, nil
}

// CacheDir returns where schemas are cached.
func CacheDir() string { return filepath.Join(paths.Cache(), "appsettings") }

// Load returns the schema from the cache, fetching it when missing. Cache
// persistence is best effort: failure logs a warning without discarding fetched
// settings. A successful return does not guarantee a durable cache entry.
func Load(ctx context.Context, appID string, version int, partNumber string) (*Schema, error) {
	if err := checkIDs(appID, partNumber); err != nil {
		return nil, err
	}

	path := filepath.Join(CacheDir(), fmt.Sprintf("%s-v%d-%s.json", strings.ToLower(appID), version, partNumber))
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // path is inside garminctl's cache directory
		var s Schema
		if json.Unmarshal(b, &s) == nil && len(s.Settings) > 0 {
			return s.index(), nil
		}
	}

	s, err := Fetch(ctx, appID, version, partNumber)
	if err != nil {
		return nil, err
	}

	if err := saveSchema(path, s); err != nil {
		slog.WarnContext(ctx, "settings schema cache write failed", "appID", appID, "version", version, "partNumber", partNumber, "error", err)
	}

	return s, nil
}

// saveSchema replaces a whole cache entry without truncating a previous entry.
func saveSchema(path string, schema *Schema) (err error) {
	data, err := json.MarshalIndent(schema, "", " ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(path), ".schema-*.tmp")
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close() // release descriptor on failure; the successful close is checked below
		if cleanupErr := os.Remove(f.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			err = errors.Join(err, cleanupErr)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(f.Name(), path)
}

// Parse extracts the settings from the page HTML.
func Parse(page []byte) (*Schema, error) {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, err
	}

	c := &formCollector{labels: map[string]string{}}
	c.walk(doc)

	if len(c.controls) == 0 {
		return nil, errors.New("no settings controls found in page")
	}

	for i := range c.controls {
		c.controls[i].Label = c.labels[c.controls[i].Key]
	}

	return (&Schema{Settings: c.controls}).index(), nil
}

// formCollector gathers the form controls and the labels of the settings page.
type formCollector struct {
	labels   map[string]string
	controls []Setting
}

func (c *formCollector) walk(n *html.Node) {
	if n.Type == html.ElementNode {
		c.element(n)
	}

	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.walk(ch)
	}
}

func (c *formCollector) element(n *html.Node) {
	switch n.Data {
	case "label":
		if k := attr(n, "for"); k != "" {
			c.labels[k] = strings.TrimSpace(text(n))
		}
	case "select":
		if k := attr(n, "name"); k != "" {
			c.controls = append(c.controls, parseSelect(k, n))
		}
	case "input":
		if st, ok := parseInput(n); ok {
			c.controls = append(c.controls, st)
		}
	case "textarea":
		if k := attr(n, "name"); k != "" {
			c.controls = append(c.controls, Setting{Key: k, Type: "text", Default: strings.TrimSpace(text(n)), ReadOnly: hasAttr(n, "readonly")})
		}
	}
}

func parseSelect(key string, n *html.Node) Setting {
	st := Setting{Key: key, Type: "select"}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "option" {
			continue
		}

		o := Option{Value: attr(c, "value"), Label: strings.TrimSpace(text(c))}
		st.Options = append(st.Options, o)

		if hasAttr(c, "selected") {
			st.Default = o.Value
		}
	}

	return st
}

// parseInput describes an <input>; hidden inputs and buttons are not settings.
func parseInput(n *html.Node) (Setting, bool) {
	k := attr(n, "name")
	if k == "" {
		return Setting{}, false
	}

	st := Setting{Key: k, Default: attr(n, "value"), ReadOnly: hasAttr(n, "readonly"), Required: hasAttr(n, "required")}

	switch attr(n, "type") {
	case "checkbox":
		st.Type = "toggle"
		st.Default = strconv.FormatBool(hasAttr(n, "checked"))
	case "number":
		st.Type = "number"
		st.Min, st.Max, st.Step = attr(n, "min"), attr(n, "max"), attr(n, "step")
	case "hidden", "submit", "button":
		return Setting{}, false
	default:
		st.Type = "text"
		if ml := attr(n, "maxlength"); ml != "" {
			st.MaxLength, _ = strconv.Atoi(ml)
		}
	}

	return st, true
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}

	return ""
}

func hasAttr(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if a.Key == name {
			return true
		}
	}

	return false
}

func text(n *html.Node) string {
	var (
		sb strings.Builder
		f  func(*html.Node)
	)

	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)

	return sb.String()
}

// LatestVersion asks the public store API for the latest internal version
// number of an app (the number used in settings page URLs).
func LatestVersion(ctx context.Context, appID string) (int, error) {
	resp, err := appRecordResponse(ctx, appID)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GET %s/apps/%s: %s", storeAPI, url.PathEscape(appID), resp.Status)
	}

	var info struct {
		LatestInternalVersion int `json:"latestInternalVersion"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return 0, err
	}

	if info.LatestInternalVersion == 0 {
		return 0, errors.New("store returned no version")
	}

	return info.LatestInternalVersion, nil
}

// AppInfoResult is the public store record of an app.
type AppInfoResult struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	LatestExternalVersion string `json:"latestExternalVersion"`
	LatestInternalVersion int    `json:"latestInternalVersion"`
	DeveloperName         string `json:"developer"`
}

// AppInfo fetches the public store record of an app.
func AppInfo(ctx context.Context, appID string) (*AppInfoResult, error) {
	resp, err := appRecordResponse(ctx, appID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("store answered %s", resp.Status)
	}

	var raw struct {
		ID                    string `json:"id"`
		LatestExternalVersion string `json:"latestExternalVersion"`
		LatestInternalVersion int    `json:"latestInternalVersion"`
		AppLocalizations      []struct {
			Name string `json:"name"`
		} `json:"appLocalizations"`
		Developer struct {
			DeveloperDisplayName string `json:"developerDisplayName"`
		} `json:"developer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, err
	}

	r := &AppInfoResult{ID: raw.ID, LatestExternalVersion: raw.LatestExternalVersion, LatestInternalVersion: raw.LatestInternalVersion, DeveloperName: raw.Developer.DeveloperDisplayName}
	if len(raw.AppLocalizations) > 0 {
		r.Name = raw.AppLocalizations[0].Name
	}

	return r, nil
}

// appRecordResponse shares request creation only. Callers close the body and
// retain their own status errors, response limits and decoding contracts.
func appRecordResponse(ctx context.Context, appID string) (*http.Response, error) {
	if err := checkAppID(appID); err != nil {
		return nil, err
	}

	endpoint := storeAPI + "/apps/" + url.PathEscape(appID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	return HTTPClient.Do(req)
}
