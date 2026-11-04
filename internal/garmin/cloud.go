package garmin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/paths"
)

// Cloud uploads watch files, delivers queued Garmin Connect files and edits
// cloud device settings. See docs/formats.md for the endpoints.
type Cloud struct {
	svc      *Service
	gc       *gc.Client
	cacheDir string
	watch    *Lazy
	epoch    ConnectionEpoch

	mu      sync.Mutex
	profile *CloudProfile
}

// NewCloud attaches a Garmin Connect client to a service.
func NewCloud(svc *Service, client *gc.Client) *Cloud {
	return &Cloud{svc: svc, gc: client, cacheDir: paths.Cache()}
}

// Client exposes the session for login handling.
func (c *Cloud) Client() *gc.Client { return c.gc }

// CloudProfile preserves the public account profile type.
type CloudProfile = gc.CloudProfile

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
	st := c.identityStatus(ctx)
	if st.Profile != nil && (c.svc != nil || c.watch != nil) {
		c.registration(ctx, st)
	}

	return st
}

// identityStatus keeps account/profile lookup independent of watch registration.
// The profile cache remains scoped to this Cloud, including Forget behavior.
func (c *Cloud) identityStatus(ctx context.Context) *CloudStatus {
	st := &CloudStatus{LoggedIn: c.gc.LoggedIn(), Email: c.gc.Email()}
	if err := c.gc.StoreError(); err != nil {
		st.Error = err.Error()
		return st
	}

	if !st.LoggedIn {
		return st
	}

	p, err := c.cachedProfile(ctx)
	if err != nil {
		st.Error = err.Error()
		if errors.Is(err, gc.ErrNotLoggedIn) {
			st.LoggedIn = false
		}

		return st
	}

	st.Profile = p

	return st
}

// cachedProfile returns the signed-in user's profile, fetching it once.
func (c *Cloud) cachedProfile(ctx context.Context) (*CloudProfile, error) {
	c.mu.Lock()
	p := c.profile
	c.mu.Unlock()

	if p != nil {
		return p, nil
	}

	sp, err := c.gc.SocialProfile(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.profile = sp
	c.mu.Unlock()

	return sp, nil
}

// registration fills in whether the connected watch is on the account.
func (c *Cloud) registration(ctx context.Context, st *CloudStatus) {
	dx, err := c.deviceXML(ctx)
	if err != nil {
		return
	}

	b, err := c.gc.Download(ctx, fmt.Sprintf("/device-service/deviceregistration/device/name/%d", dx.UnitID))
	if err == nil {
		st.DeviceName, st.Registered = strings.Trim(strings.TrimSpace(string(b)), `"`), true
		return
	}

	if he, ok := errors.AsType[*gc.HTTPError](err); ok && (he.Status == http.StatusNotFound || he.Status == http.StatusForbidden) {
		st.Registered = false
		return
	}

	st.Error = err.Error()
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

// cloudFixedOptions are the enum spellings Connect uses for a few keys. They
// are offered only when the current value is among them, because the
// spellings vary by device.
var cloudFixedOptions = map[string][]CloudOption{
	"timeFormat":       {{Value: "time_twelve_hr", Label: "12-hour"}, {Value: "time_twenty_four_hr", Label: "24-hour"}},
	"dateFormat":       {{Value: "date_month_day", Label: "Month/Day"}, {Value: "date_day_month", Label: "Day/Month"}},
	"measurementUnits": {{Value: "metric", Label: "Metric"}, {Value: "statute_us", Label: "Statute (US)"}, {Value: "statute_uk", Label: "Statute (UK)"}},
	"mountingSide":     {{Value: "LEFT", Label: "Left wrist"}, {Value: "RIGHT", Label: "Right wrist"}},
	"startOfWeek":      {{Value: "MONDAY", Label: "Monday"}, {Value: "SATURDAY", Label: "Saturday"}, {Value: "SUNDAY", Label: "Sunday"}},
	"dataRecording":    {{Value: "SMART", Label: "Smart"}, {Value: "EVERY_SECOND", Label: "Every second"}},
}

func (c *Cloud) deviceID(ctx context.Context) (uint64, error) {
	dx, err := c.deviceXML(ctx)
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

	langs := languageOptions(raw["supportedLanguages"])

	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var out []CloudSetting

	for _, k := range keys {
		v := raw[k]
		if skipCloudSetting(k, v) {
			continue
		}

		out = append(out, CloudSetting{Key: k, Value: v, Type: cloudValueType(v), Options: cloudSettingOptions(k, v, langs), Hint: cloudSettingHints[k]})
	}

	return out, nil
}

// languageOptions turns Connect's supportedLanguages list into options.
func languageOptions(v any) []CloudOption {
	ls, ok := v.([]any)
	if !ok {
		return nil
	}

	var langs []CloudOption

	for _, l := range ls {
		if m, ok := l.(map[string]any); ok {
			langs = append(langs, CloudOption{Value: m["id"], Label: fmt.Sprint(m["name"])})
		}
	}

	return langs
}

// skipCloudSetting hides keys the device does not support or that are not settings.
func skipCloudSetting(k string, v any) bool {
	if v == nil || cloudSettingSkip[k] || strings.HasPrefix(k, "supported") {
		return true
	}

	m, ok := v.(map[string]any)

	return ok && len(m) == 0
}

func cloudValueType(v any) string {
	switch v.(type) {
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		return ""
	}
}

// cloudSettingOptions lists the selectable values of a key, if known.
func cloudSettingOptions(k string, v any, langs []CloudOption) []CloudOption {
	if k == "language" {
		return langs
	}

	opts := cloudFixedOptions[k]
	if !optionsContain(opts, v) {
		return nil
	}

	return opts
}

func optionsContain(opts []CloudOption, v any) bool {
	for _, o := range opts {
		if fmt.Sprint(o.Value) == fmt.Sprint(v) {
			return true
		}
	}

	return false
}

// CloudApplyResult reports a settings change pushed through Connect.
type CloudApplyResult struct {
	Applied []string     `json:"applied"`
	Pulled  []PulledFile `json:"pulled"`
	Note    string       `json:"note"`
}

// SetSettings sends a sparse PUT to Connect, then pulls the generated
// settings-change file into GARMIN/NewFiles for application on disconnect.
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

	pulled, err := c.pullQueued(ctx, "settings-change")
	res.Pulled = pulled

	if err != nil {
		res.Note = "Saved in Garmin Connect, but the settings file could not be fetched: " + err.Error()

		return res, nil //nolint:nilerr // partial success: Connect saved the settings; the pull problem is reported in Note
	}

	if len(res.Pulled) == 0 {
		res.Note = "Saved in Garmin Connect. Connect has not queued a settings file yet; press Sync later or let the phone sync."
	} else {
		res.Note = "Saved in Garmin Connect and written to GARMIN/NewFiles; unplug the watch to apply."
	}

	return res, nil
}

// pullQueued fetches what Connect queued for the watch under one message
// type. Connect queues the file a moment after the request that caused it,
// so this tries a few times. What was pulled before an error is returned
// with it.
func (c *Cloud) pullQueued(ctx context.Context, messageType string) ([]PulledFile, error) {
	var pulled []PulledFile

	for range 4 {
		p, err := c.Pull(ctx, func(m DeviceMessage) bool { return m.MessageType == messageType })
		if err != nil {
			return pulled, err
		}

		pulled = append(pulled, p...)
		if len(p) > 0 {
			break
		}

		if err := sleepCtx(ctx, pullRetryDelay); err != nil {
			return pulled, err
		}
	}

	return pulled, nil
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

// PulledFile preserves the Connect-owned delivery result.
type PulledFile = connect.PulledFile

// Pull downloads every queued message that matches (nil = all), writes it
// where the watch expects it and tells Connect it was received.
func (c *Cloud) Pull(ctx context.Context, match func(DeviceMessage) bool) ([]PulledFile, error) {
	msgs, err := c.Messages(ctx)
	if err != nil {
		return nil, err
	}

	dx, err := c.deviceXML(ctx)
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

		if err := c.deliver(ctx, target, data); err != nil {
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

	if err := c.invalidateApps(ctx); err != nil {
		return out, err
	}

	return out, nil
}

// deliver writes a pulled file where the watch expects it; app binaries go
// through the (possibly hidden) app folder.
func (c *Cloud) deliver(ctx context.Context, target string, data []byte) error {
	return c.withWatch(ctx, func(ctx context.Context, svc *Service) error {
		if strings.HasPrefix(target, AppFolder+"/") {
			_, err := svc.WriteAppFile(ctx, path.Base(target), data)
			return err
		}

		if err := svc.ensureDir(ctx, path.Dir(target)); err != nil {
			return err
		}

		return svc.WriteFile(ctx, target, data)
	})
}

func (c *Cloud) ack(ctx context.Context, id int64) error {
	p := fmt.Sprintf("/device-service/devicemessage/message/%d?status=received", id)
	err := c.gc.SendJSON(ctx, http.MethodPut, p, nil, nil)

	if he, ok := errors.AsType[*gc.HTTPError](err); ok && (he.Status == http.StatusMethodNotAllowed || he.Status == http.StatusNotFound) {
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
		return AppFolder + "/" + randomName(4) + ".PRG"
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

// uploadTarget is one of Garmin's upload endpoints.
type uploadTarget struct {
	URL     string
	Consent bool // needs a privacy consent in Connect; Garmin answers 403 without it
	What    string
}

// uploadTargets maps supported device XML data types to Garmin upload endpoints.
var uploadTargets = map[string]uploadTarget{
	"FIT_TYPE_4":  {URL: "/upload-service/upload/activity", What: "activities"},
	"FIT_TYPE_9":  {URL: "/upload-service/upload/weight", What: "weight"},
	"FIT_TYPE_32": {URL: "/upload-service/upload/wellness", What: "daily monitoring"},
	"FIT_TYPE_35": {URL: "/upload-service/upload/segment-list", What: "segments"},
	"FIT_TYPE_38": {URL: "https://golf.garmin.com/gcs-golfcommunity/api/v2/scorecard/fit", What: "golf scorecards"},
	"FIT_TYPE_41": {URL: "/upload-service/upload/changelog", What: "settings change log"},
	"FIT_TYPE_44": {URL: "/upload-service/upload/metrics", What: "fitness metrics"},
	"FIT_TYPE_49": {URL: "/upload-service/upload/wellness", What: "sleep"},
	"FIT_TYPE_57": {URL: "/upload-service/upload/sport-backup", What: "sport profiles backup"},
	"FIT_TYPE_68": {URL: "/upload-service/upload/hrvstatus", Consent: true, What: "HRV status"},
	"FIT_TYPE_70": {URL: "/upload-service/upload/partner", What: "health snapshot"},
	"FIT_TYPE_72": {URL: "/upload-service/upload/lha-backup", What: "training backup"},
	"FIT_TYPE_73": {URL: "/upload-service/upload/skintemp", Consent: true, What: "skin temperature"},
	"FIT_TYPE_74": {URL: "/upload-service/upload/primary-training-backup", Consent: true, What: "primary training device backup"},
	"FIT_TYPE_77": {URL: "/upload-service/upload/adaptive-coaching-schedule", Consent: true, What: "coaching schedule"},
	"FIT_TYPE_79": {URL: "/upload-service/upload/sleep-disruptions", Consent: true, What: "sleep disruptions"},
}

// Upload statuses of one file.
const (
	uploadStatusUploaded  = "uploaded"
	uploadStatusDuplicate = "duplicate"
	uploadStatusFailed    = "failed"
	uploadStatusSkipped   = "skipped"
)

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

// Progress receives one line per file during Upload.
type Progress func(msg string)

// uploadRun is the state of one Upload call.
type uploadRun struct {
	c             *Cloud
	led           ledger
	res           *UploadResult
	consentDenied map[string]bool // data types Garmin refused with 403 in this run
	progress      Progress
}

// Upload sends every file the watch recorded that has not been sent from
// this computer before. Files Garmin already has (synced by the phone) come
// back as duplicates and are remembered too. Nothing is deleted from the
// watch.
func (c *Cloud) Upload(ctx context.Context, progress Progress) (*UploadResult, error) {
	dx, err := c.deviceXML(ctx)
	if err != nil {
		return nil, err
	}

	if progress == nil {
		progress = func(string) {}
	}

	led, err := c.loadLedger(dx.UnitID)
	if err != nil {
		return nil, fmt.Errorf("load upload ledger: %w", err)
	}

	run := &uploadRun{c: c, led: led, res: &UploadResult{}, consentDenied: map[string]bool{}, progress: progress}

	for _, dt := range dx.DataTypes {
		t, ok := uploadTargets[dt.Name]
		if !ok {
			continue
		}

		for _, f := range dt.Files {
			if f.Direction != "OutputFromUnit" && f.Direction != "InputOutput" {
				continue
			}

			if err := run.folder(ctx, dt.Name, t, f); err != nil {
				return run.res, err
			}
		}

		if err := c.saveLedger(dx.UnitID, run.led); err != nil {
			return run.res, fmt.Errorf("save upload ledger: %w", err)
		}
	}

	return run.res, nil
}

// folder uploads the new files of one data-type location. It returns an
// error only when the Connect session is gone; other failures are recorded.
func (r *uploadRun) folder(ctx context.Context, dtName string, t uploadTarget, f DataTypeFile) error {
	dir := strings.TrimRight(f.Path, "/")

	entries, err := r.c.list(ctx, dir)
	if err != nil {
		if !errors.Is(err, device.ErrNotFound) {
			r.fail(UploadedFile{Path: dir, Kind: t.What}, err.Error())
		}

		return nil
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	for _, e := range entries {
		if e.Dir || !strings.EqualFold(strings.TrimPrefix(path.Ext(e.Name), "."), strings.TrimPrefix(f.Extension, ".")) {
			continue
		}

		p := dir + "/" + e.Name

		stamp := fmt.Sprintf("%d|%s", e.Size, e.Modified)
		if r.led[p] == stamp {
			r.res.Skipped++
			continue
		}

		if err := r.file(ctx, dtName, t, p, e.Name, stamp); err != nil {
			return err
		}
	}

	return nil
}

// file uploads one file and records the outcome.
func (r *uploadRun) file(ctx context.Context, dtName string, t uploadTarget, p, name, stamp string) error {
	uf := UploadedFile{Path: p, Kind: t.What}
	if r.consentDenied[dtName] {
		uf.Status, uf.Detail = uploadStatusSkipped, "needs a privacy consent in Garmin Connect"
		r.res.Files = append(r.res.Files, uf)
		r.res.Skipped++

		return nil
	}

	data, err := r.c.read(ctx, p)
	if err != nil {
		r.fail(uf, err.Error())

		return nil
	}

	st, body, err := r.c.gc.Upload(ctx, t.URL, name, data)
	switch {
	case err != nil:
		if errors.Is(err, gc.ErrNotLoggedIn) {
			return err
		}

		uf.Status, uf.Detail = uploadStatusFailed, err.Error()
		r.res.Failed++
	case st == http.StatusOK || st == http.StatusCreated || st == http.StatusAccepted:
		uf.Status = uploadStatusUploaded
		if msg := importMessage(body); msg != "" {
			uf.Detail = msg
		}

		r.res.Uploaded++
		r.led[p] = stamp
	case st == http.StatusConflict:
		uf.Status, uf.Detail = uploadStatusDuplicate, "Garmin already has it"
		r.res.Duplicate++
		r.led[p] = stamp
	case st == http.StatusForbidden && t.Consent:
		uf.Status, uf.Detail = uploadStatusSkipped, "needs a privacy consent in Garmin Connect (Settings → Data & privacy)"
		r.consentDenied[dtName] = true
		r.res.Skipped++
	default:
		uf.Status, uf.Detail = uploadStatusFailed, (&gc.HTTPError{Status: st, Body: string(body), URL: t.URL}).Error()
		r.res.Failed++
	}

	r.res.Files = append(r.res.Files, uf)
	r.progress(fmt.Sprintf("%s: %s%s", p, uf.Status, ifs(uf.Detail != "", " ("+uf.Detail+")", "")))

	return nil
}

// fail records a file that could not be uploaded.
func (r *uploadRun) fail(uf UploadedFile, detail string) {
	uf.Status, uf.Detail = uploadStatusFailed, detail
	r.res.Files = append(r.res.Files, uf)
	r.res.Failed++
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
	if c.watch == nil {
		return c.sync(ctx, upload, download, progress)
	}

	bound, release, err := c.watch.BindEpoch(ctx, c.epoch)
	if err != nil {
		return nil, err
	}

	result, err := c.sync(bound, upload, download, progress)
	cause := errors.Join(context.Cause(bound), c.watch.epochCause(c.epoch))

	release()

	if cause != nil && !errors.Is(err, cause) {
		err = errors.Join(err, cause)
	}

	return result, err
}

func (c *Cloud) sync(ctx context.Context, upload, download bool, progress Progress) (*SyncResult, error) {
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
		reportPulled(progress, pulled)
	}

	return res, nil
}

// reportPulled tells the progress callback about each delivered message.
func reportPulled(progress Progress, pulled []PulledFile) {
	if progress == nil {
		return
	}

	for _, p := range pulled {
		if p.Error != "" {
			progress(fmt.Sprintf("%s (%s): %s", p.Name, p.Type, p.Error))
		} else {
			progress(fmt.Sprintf("%s (%s) -> %s", p.Name, p.Type, p.Path))
		}
	}
}
