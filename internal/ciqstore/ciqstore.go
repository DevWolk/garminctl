// Package ciqstore reads Connect IQ app settings definitions from the
// public app-settings page on apps.garmin.com, the same page Garmin Express
// renders in its embedded browser. The page is server-rendered HTML with one
// form control per setting, so it carries labels, dropdown options and
// their values.
package ciqstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
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
	cands := append(exact, partial...)
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

// Get returns the setting with the given key.
func (s *Schema) Get(key string) (*Setting, bool) {
	if s.byKey == nil {
		s.byKey = map[string]*Setting{}
		for i := range s.Settings {
			s.byKey[s.Settings[i].Key] = &s.Settings[i]
		}
	}
	st, ok := s.byKey[key]
	return st, ok
}

// PageURL is the public settings page for an app version on a device.
func PageURL(appID string, version int, partNumber string) string {
	return fmt.Sprintf("https://apps.garmin.com/en-US/appSettings2/%s/versions/%d/devices/%s/edit?singlePage=true", appID, version, partNumber)
}

// HTTPClient is used for fetching; tests may replace it.
var HTTPClient = &http.Client{Timeout: 30 * time.Second}

// Fetch downloads and parses the settings form.
func Fetch(ctx context.Context, appID string, version int, partNumber string) (*Schema, error) {
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
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "garminctl", "appsettings")
}

// Load returns the schema from the cache, fetching it when missing.
func Load(ctx context.Context, appID string, version int, partNumber string) (*Schema, error) {
	path := filepath.Join(CacheDir(), fmt.Sprintf("%s-v%d-%s.json", strings.ToLower(appID), version, partNumber))
	if b, err := os.ReadFile(path); err == nil {
		var s Schema
		if json.Unmarshal(b, &s) == nil && len(s.Settings) > 0 {
			return &s, nil
		}
	}
	s, err := Fetch(ctx, appID, version, partNumber)
	if err != nil {
		return nil, err
	}
	if b, err := json.MarshalIndent(s, "", " "); err == nil {
		_ = os.MkdirAll(CacheDir(), 0o755)
		_ = os.WriteFile(path, b, 0o644)
	}
	return s, nil
}

// Parse extracts the settings from the page HTML.
func Parse(page []byte) (*Schema, error) {
	doc, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	var controls []Setting
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "label":
				if k := attr(n, "for"); k != "" {
					labels[k] = strings.TrimSpace(text(n))
				}
			case "select":
				if k := attr(n, "name"); k != "" {
					st := Setting{Key: k, Type: "select"}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && c.Data == "option" {
							o := Option{Value: attr(c, "value"), Label: strings.TrimSpace(text(c))}
							st.Options = append(st.Options, o)
							if hasAttr(c, "selected") {
								st.Default = o.Value
							}
						}
					}
					controls = append(controls, st)
				}
			case "input":
				k := attr(n, "name")
				if k == "" {
					break
				}
				st := Setting{Key: k, Default: attr(n, "value")}
				switch attr(n, "type") {
				case "checkbox":
					st.Type = "toggle"
					st.Default = strconv.FormatBool(hasAttr(n, "checked"))
				case "number":
					st.Type = "number"
					st.Min, st.Max, st.Step = attr(n, "min"), attr(n, "max"), attr(n, "step")
				case "hidden", "submit", "button":
					k = ""
				default:
					st.Type = "text"
					if ml := attr(n, "maxlength"); ml != "" {
						st.MaxLength, _ = strconv.Atoi(ml)
					}
				}
				if k == "" {
					break
				}
				st.ReadOnly = hasAttr(n, "readonly")
				st.Required = hasAttr(n, "required")
				controls = append(controls, st)
			case "textarea":
				if k := attr(n, "name"); k != "" {
					controls = append(controls, Setting{Key: k, Type: "text", Default: strings.TrimSpace(text(n)), ReadOnly: hasAttr(n, "readonly")})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(controls) == 0 {
		return nil, errors.New("no settings controls found in page")
	}
	for i := range controls {
		controls[i].Label = labels[controls[i].Key]
	}
	return &Schema{Settings: controls}, nil
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
	var sb strings.Builder
	var f func(*html.Node)
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
	url := "https://apps.garmin.com/api/appsLibraryExternalServices/api/asw/apps/" + appID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GET %s: %s", url, resp.Status)
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
	url := "https://apps.garmin.com/api/appsLibraryExternalServices/api/asw/apps/" + appID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := HTTPClient.Do(req)
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
