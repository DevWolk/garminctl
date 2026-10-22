package garmin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"garminctl/internal/device"
	"garminctl/internal/gc"
)

// Cloud wires a Garmin Connect session to the watch: it uploads what the
// watch recorded, pulls what Connect queued for the watch, and edits the
// device settings Connect keeps in the cloud. The protocol is the one Garmin
// Express uses (see docs/formats.md, "Garmin Connect sync").
type Cloud struct {
	svc      *Service
	gc       *gc.Client
	cacheDir string

	mu      sync.Mutex
	profile *CloudProfile
}

// NewCloud attaches a Garmin Connect client to a service.
func NewCloud(svc *Service, client *gc.Client) *Cloud {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return &Cloud{svc: svc, gc: client, cacheDir: filepath.Join(dir, "garminctl")}
}

// Client exposes the session for login handling.
func (c *Cloud) Client() *gc.Client { return c.gc }

// CloudProfile is the signed-in user's public profile. Garmin's displayName
// is an opaque id for most accounts; Name holds the best human label.
type CloudProfile struct {
	DisplayName string `json:"displayName"`
	FullName    string `json:"fullName"`
	UserName    string `json:"userName"`
	Name        string `json:"name"`
}

// CloudStatus is what the UI shows at the top of the Garmin Connect tab.
type CloudStatus struct {
	LoggedIn   bool          `json:"loggedIn"`
	Email      string        `json:"email,omitempty"`
	Profile    *CloudProfile `json:"profile,omitempty"`
	DeviceName string        `json:"deviceName,omitempty"`
	Registered bool          `json:"deviceRegistered"`
	Error      string        `json:"error,omitempty"`
}

// Status reports the session and whether this watch is on the account.
func (c *Cloud) Status(ctx context.Context) *CloudStatus {
	st := &CloudStatus{LoggedIn: c.gc.LoggedIn(), Email: c.gc.Email()}
	if !st.LoggedIn {
		return st
	}
	c.mu.Lock()
	p := c.profile
	c.mu.Unlock()
	if p == nil {
		var sp CloudProfile
		if err := c.gc.GetJSON(ctx, "/userprofile-service/socialProfile", &sp); err != nil {
			st.Error = err.Error()
			if errors.Is(err, gc.ErrNotLoggedIn) {
				st.LoggedIn = false
			}
			return st
		}
		sp.Name = sp.FullName
		if sp.Name == "" {
			sp.Name = sp.UserName
		}
		if sp.Name == "" && !strings.Contains(sp.DisplayName, "-") {
			sp.Name = sp.DisplayName
		}
		p = &sp
		c.mu.Lock()
		c.profile = p
		c.mu.Unlock()
	}
	st.Profile = p
	if c.svc != nil {
		if dx, err := c.svc.DeviceXML(ctx); err == nil {
			if b, err := c.gc.Download(ctx, fmt.Sprintf("/device-service/deviceregistration/device/name/%d", dx.UnitID)); err == nil {
				st.DeviceName, st.Registered = strings.Trim(strings.TrimSpace(string(b)), `"`), true
			} else {
				var he *gc.HTTPError
				if errors.As(err, &he) && (he.Status == 404 || he.Status == 403) {
					st.Registered = false
				} else {
					st.Error = err.Error()
				}
			}
		}
	}
	return st
}

// Forget drops cached profile data (after logout).
func (c *Cloud) Forget() {
	c.mu.Lock()
	c.profile = nil
	c.mu.Unlock()
}

// ---------- device settings kept by Garmin Connect ----------

// CloudSetting is one key of Connect's device settings record.
type CloudSetting struct {
	Key     string        `json:"key"`
	Value   any           `json:"value"`
	Type    string        `json:"type"` // string, bool, number, list, object
	Options []CloudOption `json:"options,omitempty"`
	Hint    string        `json:"hint,omitempty"`
}

// CloudOption is a selectable value.
type CloudOption struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

var cloudSettingHints = map[string]string{
	"timeFormat":                "Also in Watch settings as deviceSettings.timeMode; Connect pushes this copy back on every sync.",
	"dateFormat":                "Connect's copy of the date format.",
	"measurementUnits":          "statute or metric; overrides the per-unit choices on the watch.",
	"mountingSide":              "Wrist the watch is worn on.",
	"language":                  "Watch language; the number is Connect's language id.",
	"autoSyncStepsBeforeSync":   "Auto-sync after this many steps.",
	"autoSyncMinutesBeforeSync": "Auto-sync after this many minutes.",
	"alarms":                    "Alarm list in Connect's JSON form.",
	"connectIQ":                 "autoUpdate: let Connect update Connect IQ apps automatically.",
}

var cloudSettingSkip = map[string]bool{"deviceId": true, "supportedLanguages": true, "supportedAlarmModes": true, "supportedAudioPromptDialects": true, "enabledScreens": true, "visibleScreens": true, "screenLists": true}

func (c *Cloud) deviceID(ctx context.Context) (uint64, error) {
	dx, err := c.svc.DeviceXML(ctx)
	if err != nil {
		return 0, err
	}
	return dx.UnitID, nil
}

// Settings reads the device settings record Garmin Connect keeps for this
// watch. Null keys (not supported by the device) are left out.
func (c *Cloud) Settings(ctx context.Context) ([]CloudSetting, error) {
	id, err := c.deviceID(ctx)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/device-service/deviceservice/device-info/settings/%d", id), &raw); err != nil {
		return nil, err
	}
	var langs []CloudOption
	if ls, ok := raw["supportedLanguages"].([]any); ok {
		for _, l := range ls {
			if m, ok := l.(map[string]any); ok {
				langs = append(langs, CloudOption{Value: m["id"], Label: fmt.Sprint(m["name"])})
			}
		}
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []CloudSetting
	for _, k := range keys {
		v := raw[k]
		if v == nil || cloudSettingSkip[k] || strings.HasPrefix(k, "supported") {
			continue
		}
		if m, ok := v.(map[string]any); ok && len(m) == 0 {
			continue
		}
		s := CloudSetting{Key: k, Value: v, Hint: cloudSettingHints[k]}
		switch v.(type) {
		case bool:
			s.Type = "bool"
		case float64:
			s.Type = "number"
		case string:
			s.Type = "string"
		case []any:
			s.Type = "list"
		case map[string]any:
			s.Type = "object"
		}
		if k == "language" && len(langs) > 0 {
			s.Options = langs
		}
		switch k {
		case "timeFormat":
			s.Options = []CloudOption{{"time_twelve_hr", "12-hour"}, {"time_twenty_four_hr", "24-hour"}}
		case "dateFormat":
			s.Options = []CloudOption{{"date_month_day", "Month/Day"}, {"date_day_month", "Day/Month"}}
		case "measurementUnits":
			s.Options = []CloudOption{{"metric", "Metric"}, {"statute_us", "Statute (US)"}, {"statute_uk", "Statute (UK)"}}
		case "mountingSide":
			s.Options = []CloudOption{{"LEFT", "Left wrist"}, {"RIGHT", "Right wrist"}}
		case "startOfWeek":
			s.Options = []CloudOption{{"MONDAY", "Monday"}, {"SATURDAY", "Saturday"}, {"SUNDAY", "Sunday"}}
		case "dataRecording":
			s.Options = []CloudOption{{"SMART", "Smart"}, {"EVERY_SECOND", "Every second"}}
		}
		// Only trust an option list that contains the current value; Connect's enum spellings vary by device.
		if len(s.Options) > 0 && k != "language" {
			found := false
			for _, o := range s.Options {
				if fmt.Sprint(o.Value) == fmt.Sprint(v) {
					found = true
				}
			}
			if !found {
				s.Options = nil
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// CloudApplyResult reports a settings change pushed through Connect.
type CloudApplyResult struct {
	Applied []string     `json:"applied"`
	Pulled  []PulledFile `json:"pulled"`
	Note    string       `json:"note"`
}

// SetSettings changes keys of Connect's record (a sparse PUT, as Garmin
// Express does), then pulls the settings-change file Connect generates into
// GARMIN/NewFiles so the watch applies it on unplug.
func (c *Cloud) SetSettings(ctx context.Context, changes map[string]any) (*CloudApplyResult, error) {
	if len(changes) == 0 {
		return nil, errors.New("nothing to change")
	}
	id, err := c.deviceID(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"deviceId": id}
	var keys []string
	for k, v := range changes {
		if cloudSettingSkip[k] {
			return nil, fmt.Errorf("%s cannot be changed", k)
		}
		body[k] = v
		keys = append(keys, k)
	}
	sort.Strings(keys)
	p := fmt.Sprintf("/device-service/deviceservice/device-info/settings/%d", id)
	if err := c.gc.SendJSON(ctx, http.MethodPut, p, body, nil); err != nil {
		return nil, err
	}
	res := &CloudApplyResult{Applied: keys}
	// Connect queues a settings-change file for the watch; give it a moment.
	for attempt := 0; attempt < 4; attempt++ {
		pulled, err := c.Pull(ctx, func(m DeviceMessage) bool { return m.MessageType == "settings-change" })
		if err != nil {
			res.Note = "Saved in Garmin Connect, but the settings file could not be fetched: " + err.Error()
			return res, nil
		}
		res.Pulled = append(res.Pulled, pulled...)
		if len(pulled) > 0 {
			break
		}
		time.Sleep(1500 * time.Millisecond)
	}
	if len(res.Pulled) == 0 {
		res.Note = "Saved in Garmin Connect. Connect has not queued a settings file yet; press Sync later or let the phone sync."
	} else {
		res.Note = "Saved in Garmin Connect and written to GARMIN/NewFiles; unplug the watch to apply."
	}
	return res, nil
}

// ---------- device messages (what Connect queued for the watch) ----------

// DeviceMessage is one item Garmin Connect wants delivered to the watch.
type DeviceMessage struct {
	MessageID         int64  `json:"messageId"`
	MessageType       string `json:"messageType"`
	MessageStatus     string `json:"messageStatus"`
	DeviceID          int64  `json:"deviceId"`
	DeviceName        string `json:"deviceName"`
	DeviceXMLDataType string `json:"deviceXmlDataType"`
	MetaData          struct {
		FileType    string `json:"fileType"`
		MessageURL  string `json:"messageUrl"`
		Absolute    bool   `json:"absolute"`
		MessageName string `json:"messageName"`
		AppDetails  any    `json:"appDetails"`
	} `json:"metaData"`
}

// Messages lists what Connect has queued for this watch.
func (c *Cloud) Messages(ctx context.Context) ([]DeviceMessage, error) {
	id, err := c.deviceID(ctx)
	if err != nil {
		return nil, err
	}
	var r struct {
		Messages []DeviceMessage `json:"messages"`
	}
	if err := c.gc.GetJSON(ctx, fmt.Sprintf("/device-service/devicemessage/messages?device_id=%d", id), &r); err != nil {
		return nil, err
	}
	return r.Messages, nil
}

// PulledFile is one message delivered to the watch.
type PulledFile struct {
	MessageID int64  `json:"messageId"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	Error     string `json:"error,omitempty"`
}

// Pull downloads every queued message that matches (nil = all), writes it
// where the watch expects it and tells Connect it was received.
func (c *Cloud) Pull(ctx context.Context, match func(DeviceMessage) bool) ([]PulledFile, error) {
	msgs, err := c.Messages(ctx)
	if err != nil {
		return nil, err
	}
	dx, err := c.svc.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}
	var out []PulledFile
	for _, m := range msgs {
		if m.MessageStatus != "new" && m.MessageStatus != "" {
			continue
		}
		if match != nil && !match(m) {
			continue
		}
		pf := PulledFile{MessageID: m.MessageID, Type: m.MessageType, Name: m.MetaData.MessageName}
		data, err := c.gc.Download(ctx, m.MetaData.MessageURL)
		if err != nil {
			pf.Error = err.Error()
			out = append(out, pf)
			continue
		}
		target := targetFor(dx, m)
		if target == "" {
			pf.Error = fmt.Sprintf("no place on the watch for %s files (%s); left in Connect's queue", m.MetaData.FileType, m.DeviceXMLDataType)
			out = append(out, pf)
			continue
		}
		if strings.HasPrefix(target, "GARMIN/APPS/") {
			if _, err := c.svc.dev.Stat(ctx, path.Dir(target)); err != nil {
				pf.Error = "this watch does not expose its app folder over USB; left in Connect's queue for the phone app to deliver"
				out = append(out, pf)
				continue
			}
		} else if err := c.svc.ensureDir(ctx, path.Dir(target)); err != nil {
			pf.Error = err.Error()
			out = append(out, pf)
			continue
		}
		if err := c.svc.dev.Write(ctx, target, data); err != nil {
			pf.Error = err.Error()
			out = append(out, pf)
			continue
		}
		pf.Path, pf.Bytes = target, len(data)
		if err := c.ack(ctx, m.MessageID); err != nil {
			pf.Error = "written, but Connect was not told: " + err.Error()
		}
		out = append(out, pf)
	}
	c.svc.invalidate(PathAppsConfig)
	return out, nil
}

func (c *Cloud) ack(ctx context.Context, id int64) error {
	p := fmt.Sprintf("/device-service/devicemessage/message/%d?status=received", id)
	err := c.gc.SendJSON(ctx, http.MethodPut, p, nil, nil)
	var he *gc.HTTPError
	if errors.As(err, &he) && (he.Status == 405 || he.Status == 404) {
		err = c.gc.SendJSON(ctx, http.MethodPost, p, nil, nil)
	}
	return err
}

func randomName(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

// targetFor picks the watch path for a message from the device XML.
func targetFor(dx *DeviceXML, m DeviceMessage) string {
	ft := strings.ToUpper(m.MetaData.FileType)
	if ft == "BRAIN" || ft == "PRG" {
		return "GARMIN/APPS/MEDIA/" + randomName(4) + ".PRG"
	}
	if ft == "FIT" {
		return PathNewFiles + "/" + newUUID() + ".FIT"
	}
	for _, dt := range dx.DataTypes {
		if !strings.EqualFold(dt.Name, m.DeviceXMLDataType) {
			continue
		}
		for _, f := range dt.Files {
			if f.Direction == "InputToUnit" || f.Direction == "InputOutput" {
				ext := strings.TrimPrefix(f.Extension, ".")
				return strings.TrimRight(f.Path, "/") + "/" + randomName(4) + "." + ext
			}
		}
	}
	return ""
}

// ---------- uploads (what the watch recorded) ----------

// uploadTargets maps the device XML data type to Garmin's upload endpoint,
// as the Garmin Express upload configuration lists them. Types that need a
// privacy consent in Connect are marked; Garmin answers 403 without it.
var uploadTargets = map[string]struct {
	URL     string
	Consent bool
	What    string
}{
	"FIT_TYPE_4":  {"/upload-service/upload/activity", false, "activities"},
	"FIT_TYPE_9":  {"/upload-service/upload/weight", false, "weight"},
	"FIT_TYPE_32": {"/upload-service/upload/wellness", false, "daily monitoring"},
	"FIT_TYPE_35": {"/upload-service/upload/segment-list", false, "segments"},
	"FIT_TYPE_38": {"https://golf.garmin.com/gcs-golfcommunity/api/v2/scorecard/fit", false, "golf scorecards"},
	"FIT_TYPE_41": {"/upload-service/upload/changelog", false, "settings change log"},
	"FIT_TYPE_44": {"/upload-service/upload/metrics", false, "fitness metrics"},
	"FIT_TYPE_49": {"/upload-service/upload/wellness", false, "sleep"},
	"FIT_TYPE_57": {"/upload-service/upload/sport-backup", false, "sport profiles backup"},
	"FIT_TYPE_68": {"/upload-service/upload/hrvstatus", true, "HRV status"},
	"FIT_TYPE_70": {"/upload-service/upload/partner", false, "health snapshot"},
	"FIT_TYPE_72": {"/upload-service/upload/lha-backup", false, "training backup"},
	"FIT_TYPE_73": {"/upload-service/upload/skintemp", true, "skin temperature"},
	"FIT_TYPE_74": {"/upload-service/upload/primary-training-backup", true, "primary training device backup"},
	"FIT_TYPE_77": {"/upload-service/upload/adaptive-coaching-schedule", true, "coaching schedule"},
	"FIT_TYPE_79": {"/upload-service/upload/sleep-disruptions", true, "sleep disruptions"},
}

// UploadedFile is one file sent to Connect.
type UploadedFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Status string `json:"status"` // uploaded, duplicate, failed, skipped
	Detail string `json:"detail,omitempty"`
}

// UploadResult summarises a sync-up run.
type UploadResult struct {
	Files     []UploadedFile `json:"files"`
	Uploaded  int            `json:"uploaded"`
	Duplicate int            `json:"duplicate"`
	Failed    int            `json:"failed"`
	Skipped   int            `json:"skipped"`
}

type ledger map[string]string // watch path -> "size|modified"

func (c *Cloud) ledgerPath(unit uint64) string {
	return filepath.Join(c.cacheDir, fmt.Sprintf("uploaded-%d.json", unit))
}

func (c *Cloud) loadLedger(unit uint64) ledger {
	l := ledger{}
	if b, err := os.ReadFile(c.ledgerPath(unit)); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

func (c *Cloud) saveLedger(unit uint64, l ledger) {
	_ = os.MkdirAll(c.cacheDir, 0o700)
	b, _ := json.MarshalIndent(l, "", " ")
	_ = os.WriteFile(c.ledgerPath(unit), b, 0o600)
}

// Progress receives one line per file during Upload.
type Progress func(msg string)

// Upload sends every file the watch recorded that has not been sent from
// this computer before. Files Garmin already has (synced by the phone) come
// back as duplicates and are remembered too. Nothing is deleted from the
// watch.
func (c *Cloud) Upload(ctx context.Context, progress Progress) (*UploadResult, error) {
	dx, err := c.svc.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		progress = func(string) {}
	}
	led := c.loadLedger(dx.UnitID)
	res := &UploadResult{}
	consentDenied := map[string]bool{}
	for _, dt := range dx.DataTypes {
		t, ok := uploadTargets[dt.Name]
		if !ok {
			continue
		}
		for _, f := range dt.Files {
			if f.Direction != "OutputFromUnit" && f.Direction != "InputOutput" {
				continue
			}
			dir := strings.TrimRight(f.Path, "/")
			entries, err := c.svc.dev.List(ctx, dir)
			if err != nil {
				if errors.Is(err, device.ErrNotFound) {
					continue
				}
				res.Files = append(res.Files, UploadedFile{Path: dir, Kind: t.What, Status: "failed", Detail: err.Error()})
				res.Failed++
				continue
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			for _, e := range entries {
				if e.Dir || !strings.EqualFold(strings.TrimPrefix(path.Ext(e.Name), "."), strings.TrimPrefix(f.Extension, ".")) {
					continue
				}
				p := dir + "/" + e.Name
				stamp := fmt.Sprintf("%d|%s", e.Size, e.Modified)
				if led[p] == stamp {
					res.Skipped++
					continue
				}
				uf := UploadedFile{Path: p, Kind: t.What}
				if consentDenied[dt.Name] {
					uf.Status, uf.Detail = "skipped", "needs a privacy consent in Garmin Connect"
					res.Files = append(res.Files, uf)
					res.Skipped++
					continue
				}
				data, err := c.svc.dev.Read(ctx, p)
				if err != nil {
					uf.Status, uf.Detail = "failed", err.Error()
					res.Files = append(res.Files, uf)
					res.Failed++
					continue
				}
				st, body, err := c.gc.Upload(ctx, t.URL, e.Name, data)
				switch {
				case err != nil:
					if errors.Is(err, gc.ErrNotLoggedIn) {
						return res, err
					}
					uf.Status, uf.Detail = "failed", err.Error()
					res.Failed++
				case st == 200 || st == 201 || st == 202:
					uf.Status = "uploaded"
					if msg := importMessage(body); msg != "" {
						uf.Detail = msg
					}
					res.Uploaded++
					led[p] = stamp
				case st == 409:
					uf.Status, uf.Detail = "duplicate", "Garmin already has it"
					res.Duplicate++
					led[p] = stamp
				case st == 403 && t.Consent:
					uf.Status, uf.Detail = "skipped", "needs a privacy consent in Garmin Connect (Settings → Data & privacy)"
					consentDenied[dt.Name] = true
					res.Skipped++
				default:
					uf.Status, uf.Detail = "failed", (&gc.HTTPError{Status: st, Body: string(body), URL: t.URL}).Error()
					res.Failed++
				}
				res.Files = append(res.Files, uf)
				progress(fmt.Sprintf("%s: %s%s", p, uf.Status, ifs(uf.Detail != "", " ("+uf.Detail+")", "")))
			}
		}
		c.saveLedger(dx.UnitID, led)
	}
	return res, nil
}

func ifs(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// importMessage digs the human line out of an upload-service answer.
func importMessage(body []byte) string {
	var r struct {
		DetailedImportResult struct {
			Successes []struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			} `json:"successes"`
			Failures []struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			} `json:"failures"`
		} `json:"detailedImportResult"`
	}
	if json.Unmarshal(body, &r) != nil {
		return ""
	}
	for _, s := range r.DetailedImportResult.Successes {
		for _, m := range s.Messages {
			if m.Content != "" {
				return m.Content
			}
		}
	}
	for _, f := range r.DetailedImportResult.Failures {
		for _, m := range f.Messages {
			if m.Content != "" {
				return m.Content
			}
		}
	}
	return ""
}

// SyncResult is a full sync: upload, then pull.
type SyncResult struct {
	Upload *UploadResult `json:"upload,omitempty"`
	Pulled []PulledFile  `json:"pulled"`
	Errors []string      `json:"errors,omitempty"`
}

// Sync uploads new files and pulls queued messages.
func (c *Cloud) Sync(ctx context.Context, upload, download bool, progress Progress) (*SyncResult, error) {
	if !c.gc.LoggedIn() {
		return nil, gc.ErrNotLoggedIn
	}
	res := &SyncResult{}
	if upload {
		up, err := c.Upload(ctx, progress)
		if err != nil {
			if errors.Is(err, gc.ErrNotLoggedIn) {
				return nil, err
			}
			res.Errors = append(res.Errors, "upload: "+err.Error())
		}
		res.Upload = up
	}
	if download {
		pulled, err := c.Pull(ctx, nil)
		if err != nil {
			if errors.Is(err, gc.ErrNotLoggedIn) {
				return nil, err
			}
			res.Errors = append(res.Errors, "download: "+err.Error())
		}
		res.Pulled = pulled
		for _, p := range pulled {
			if progress != nil {
				if p.Error != "" {
					progress(fmt.Sprintf("%s (%s): %s", p.Name, p.Type, p.Error))
				} else {
					progress(fmt.Sprintf("%s (%s) -> %s", p.Name, p.Type, p.Path))
				}
			}
		}
	}
	return res, nil
}
