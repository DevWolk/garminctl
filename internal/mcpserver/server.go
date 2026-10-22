// Package mcpserver exposes the watch to AI assistants over MCP (stdio).
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
)

const (
	maxDecodedMsgs    = 400
	defaultActivities = 30
	defaultPoints     = 200
	storePageSize     = 20
)

// server holds the lazily connected watch the tools share.
type server struct {
	svc *garmin.Lazy
}

const instructions = `garminctl gives direct access to a Garmin watch connected over USB (no Garmin Express, no cloud).

Settings live in FIT messages. Address a field as message + field, by profile name when documented
(deviceSettings.backlightMode, userProfile.weight) or by number for undocumented ones ("2"."220").
Use watch_settings_schema to list documented fields, their types and enum options before changing a value.
watch_set_settings writes a settings-change file into GARMIN/NewFiles; the watch applies it when the USB
session ends (user unplugs or ejects the watch). Connect IQ app and watch-face settings (for example the
Easy+ watch face) are edited with watch_app_settings / watch_set_app_settings and take effect immediately.
watch_check_updates asks Garmin's update service (what Garmin Express uses) for firmware and data updates
and compares installed Connect IQ apps with the store; watch_install_update downloads one update, verifies
it and writes it to the watch (installed on unplug; firmware restarts the watch). Connect IQ apps and updates reach
the watch through Garmin Connect's queue (cloud_sync writes queued .PRG files into the hidden app folder over
raw PTP; the watch registers them on unplug) or as a sideloaded .PRG from the web UI; the store website itself
no longer queues installs, the Connect IQ phone app does.
Garmin Connect (the cloud) is reachable once the user has signed in through the web UI or "garminctl cloud login"
(never ask for the password here): cloud_status, cloud_sync (upload recorded activities/health files, pull what
Connect queued for the watch: settings changes, Connect IQ app installs/updates, workouts, courses), cloud_settings
and cloud_set_settings (the device settings Connect keeps and pushes back on every sync; changing them here is how
to stop Connect reverting a watch setting).
Id flow: watch_apps gives app names/file ids for the watch_app_* and watch_remove_app tools; watch_check_updates
gives partNumber for watch_install_update; activities_list gives file for activity_detail; workouts_list gives id
for workout_send; music_list gives track paths for music_playlist; store_search gives id for store_app.`

// withWatch adapts a tool body that needs the connected watch.
func withWatch[In, Out any](s *server, body func(ctx context.Context, svc *garmin.Service, in In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		svc, err := s.svc.Get(ctx)
		if err != nil {
			var zero Out

			return nil, zero, err
		}

		out, err := body(ctx, svc, in) // the SDK turns a non-nil err into an error result and drops out

		return nil, out, err
	}
}

// readOnly marks a tool that changes nothing; network says whether it
// reaches beyond the watch (Garmin's services).
func readOnly(title string, network bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: new(network)}
}

// writes marks a tool that changes the watch or the Garmin account.
func writes(title string, destructive, network bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: new(destructive), OpenWorldHint: new(network)}
}

// Serve runs the MCP server until ctx ends. version is reported to clients.
func Serve(ctx context.Context, open garmin.Opener, version string) error {
	s := &server{svc: garmin.NewLazy(open)}
	defer s.svc.Drop()

	srv := newServer(s, version, gc.New(gc.DefaultStorePath()))

	return srv.Run(ctx, &mcp.StdioTransport{})
}

// newServer builds the MCP server with every tool registered.
func newServer(s *server, version string, cloudClient *gc.Client) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "garminctl", Version: version}, &mcp.ServerOptions{Instructions: instructions})

	addWatchTools(srv, s)
	addAppTools(srv, s)
	addCloudTools(srv, s, cloudClient)
	addStoreTools(srv, s)
	addHealthTools(srv, s, cloudClient)
	addWorkoutTools(srv, s, cloudClient)
	addMusicTools(srv, s)

	return srv
}

type empty struct{}

type listIn struct {
	Path string `json:"path" jsonschema:"directory on the watch, e.g. GARMIN/Activity; empty for the storage root"`
}

type pathIn struct {
	Path string `json:"path" jsonschema:"file path on the watch, e.g. GARMIN/Sports/WalkingWalk.fit"`
}

type settingsIn struct {
	IncludeUnknown bool   `json:"includeUnknown,omitempty" jsonschema:"also return undocumented fields (numbers only)"`
	Message        string `json:"message,omitempty" jsonschema:"restrict to one message, e.g. deviceSettings or userProfile"`
}

type schemaIn struct {
	Message string `json:"message" jsonschema:"message name or number, e.g. deviceSettings, userProfile, hrZone, zonesTarget"`
}

type setIn struct {
	Changes []garmin.Change `json:"changes" jsonschema:"field changes; each has mesg (name or number), field (name or number), value (enum name, number or scaled value such as 79.5 for weight), optional index for repeated messages and arrayIndex for array fields"`
}

type deletedResult struct {
	Deleted string `json:"deleted"`
}

// MCP output schemas must be objects, so list results are wrapped.
type filesResult struct {
	Entries []device.Entry `json:"entries"`
}

type messagesResult struct {
	Messages  []fit.NamedMessage `json:"messages"`
	Truncated bool               `json:"truncated,omitempty"`
}

type settingsResult struct {
	Rows []garmin.SettingsRow `json:"rows"`
}

type fieldsResult struct {
	Fields []garmin.SchemaField `json:"fields"`
}

type appsResult struct {
	Apps []garmin.App `json:"apps"`
}

func addWatchTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_info",
		Description: "Summary of the connected watch: model, unit id, firmware, installed app count, pending NewFiles, storage.",
		Annotations: readOnly("Watch summary", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (*garmin.Summary, error) {
		return svc.Summary(ctx)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_list_files",
		Description: "List a directory on the watch (names, sizes, modification times). Paths from here feed watch_decode_fit and watch_delete_file.",
		Annotations: readOnly("List watch files", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in listIn) (filesResult, error) {
		entries, err := svc.Device().List(ctx, in.Path)
		return filesResult{Entries: entries}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_decode_fit",
		Description: "Decode any FIT file on the watch (settings, sport profiles, goals, activities) into named messages and fields. Undocumented fields are reported by number. At most 400 messages are returned.",
		Annotations: readOnly("Decode FIT file", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in pathIn) (messagesResult, error) {
		msgs, err := svc.DecodeFIT(ctx, in.Path)
		if err != nil {
			return messagesResult{}, err
		}

		out := messagesResult{Messages: msgs}
		if len(msgs) > maxDecodedMsgs {
			out.Messages, out.Truncated = msgs[:maxDecodedMsgs], true
		}

		return out, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_delete_file",
		Description: "Delete one file on the watch permanently; there is no undo. Mainly for removing pending files from GARMIN/NewFiles. For Connect IQ apps use watch_remove_app instead.",
		Annotations: writes("Delete watch file", true, false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in pathIn) (deletedResult, error) {
		if err := svc.Device().Delete(ctx, in.Path); err != nil {
			return deletedResult{}, err
		}

		svc.Refresh()

		return deletedResult{Deleted: in.Path}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_settings",
		Description: "Read the watch's own device settings (GARMIN/Settings/Settings.fit): device settings, user profile, HR zones, alarms, sensors and more, one row per field with value, type, enum options and notes. Not for Connect IQ apps (watch_app_settings) and not Garmin Connect's copy (cloud_settings).",
		Annotations: readOnly("Read watch settings", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in settingsIn) (settingsResult, error) {
		view, err := svc.Settings(ctx)
		if err != nil {
			return settingsResult{}, err
		}

		return settingsResult{Rows: filterRows(view.Rows(), in)}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_settings_schema",
		Description: "List the documented fields of a FIT settings message with types, units and enum options. Use before watch_set_settings.",
		Annotations: readOnly("Settings field schema", false),
	}, withWatch(s, func(_ context.Context, svc *garmin.Service, in schemaIn) (fieldsResult, error) {
		fields, err := svc.Schema(in.Message)
		return fieldsResult{Fields: fields}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_set_settings",
		Description: "Change watch settings. Writes a Garmin settings-change file into GARMIN/NewFiles; the watch applies it when the USB session ends, so tell the user to unplug or eject the watch afterwards. Returns the resolved changes with previous values.",
		Annotations: writes("Change watch settings", false, false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in setIn) (*garmin.ApplyResult, error) {
		return svc.ApplyChanges(ctx, in.Changes)
	}))
}

// filterRows keeps the settings rows the caller asked for.
func filterRows(rows []garmin.SettingsRow, in settingsIn) []garmin.SettingsRow {
	out := make([]garmin.SettingsRow, 0, len(rows))

	for _, r := range rows {
		if !in.IncludeUnknown && !r.Known && r.Note == "" {
			continue
		}

		if in.Message != "" && r.Mesg != in.Message && strconv.FormatUint(uint64(r.MesgNum), 10) != in.Message {
			continue
		}

		out = append(out, r)
	}

	return out
}

type appIn struct {
	App string `json:"app" jsonschema:"app name (Easy+), file name (G5RG4154) or store id, as listed by watch_apps"`
}

type appSchemaIn struct {
	App string `json:"app" jsonschema:"app name (Easy+), file name (G5RG4154) or store id, as listed by watch_apps"`
	Key string `json:"key,omitempty" jsonschema:"restrict to one setting key; omit for all"`
}

type appSetIn struct {
	App    string         `json:"app" jsonschema:"app name (Easy+), file name (G5RG4154) or store id, as listed by watch_apps"`
	Values map[string]any `json:"values" jsonschema:"keys to change with their new values; for dropdowns pass the option label (e.g. \"Body Battery %\") or its value, for toggles on/off, colors as RRGGBB hex strings where the schema says so"`
}

type installIn struct {
	PartNumber string `json:"partNumber" jsonschema:"part number of an update returned by watch_check_updates, e.g. 006-B4426-00"`
}

type hideIn struct {
	App    string `json:"app" jsonschema:"the entry's name, file id or store id from watch_apps"`
	Hidden bool   `json:"hidden" jsonschema:"true removes it from the watch's activity list / glance loop / apps, false puts it back"`
}

type rmIn struct {
	App string `json:"app" jsonschema:"the app's name, file id or store id from watch_apps"`
}

type updatesResult struct {
	Software      []garmin.SoftwareUpdate `json:"software"`
	Apps          []garmin.AppUpdate      `json:"apps"`
	SoftwareError string                  `json:"softwareError,omitempty"`
	AppsError     string                  `json:"appsError,omitempty"`
}

type hiddenResult struct {
	Hidden bool   `json:"hidden"`
	Backup string `json:"backup"`
	Note   string `json:"note"`
}

type removedResult struct {
	Removed []string `json:"removed"`
	Note    string   `json:"note"`
}

func addAppTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_apps",
		Description: "List installed apps, watch faces, glances and activities with their hidden flag. Connect IQ apps have a fileName (e.g. G5RG4154 for Easy+) used by the app settings tools.",
		Annotations: readOnly("List watch apps", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (appsResult, error) {
		apps, err := svc.Apps(ctx)
		return appsResult{Apps: apps}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_app_settings",
		Description: "Read a Connect IQ app's or watch face's settings: for each key the human label, type (select/toggle/text/number), current value and, for dropdowns, the label of the current choice. Use watch_app_settings_schema to see the available dropdown options. Not for the watch's own settings (watch_settings).",
		Annotations: readOnly("Read app settings", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in appIn) (*garmin.AppSettings, error) {
		return svc.AppSettings(ctx, in.App)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_app_settings_schema",
		Description: "Settings definition of a Connect IQ app as Garmin's store publishes it: labels, dropdown options with their values, number ranges, read-only markers. Large for apps with many dropdowns; pass key to narrow to one setting.",
		Annotations: readOnly("App settings schema", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in appSchemaIn) (*ciqstore.Schema, error) {
		sc, err := svc.AppSchema(ctx, in.App)
		if err != nil {
			return nil, err
		}

		if in.Key == "" {
			return sc, nil
		}

		st, ok := sc.Get(in.Key)
		if !ok {
			return nil, fmt.Errorf("no setting %q", in.Key)
		}

		return &ciqstore.Schema{Source: sc.Source, Settings: []ciqstore.Setting{*st}}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_set_app_settings",
		Description: "Change Connect IQ app or watch-face settings and write the .SET file back to the watch. Values are validated against the store's settings definition (dropdown labels are accepted). Takes effect immediately.",
		Annotations: writes("Change app settings", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in appSetIn) (*garmin.AppSettings, error) {
		return svc.SetAppSettings(ctx, in.App, in.Values)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_check_updates",
		Description: "Check Garmin's update service for firmware and data updates for this watch (the same service Garmin Express uses) and compare installed Connect IQ apps with the store. Returns both lists; nothing is installed. partNumber from the software list feeds watch_install_update.",
		Annotations: readOnly("Check for updates", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (updatesResult, error) {
		sw, swErr := svc.CheckSoftwareUpdates(ctx)
		apps, appErr := svc.CheckAppUpdates(ctx)

		out := updatesResult{Software: sw, Apps: apps}
		if swErr != nil {
			out.SoftwareError = swErr.Error()
		}

		if appErr != nil {
			out.AppsError = appErr.Error()
		}

		return out, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_set_app_hidden",
		Description: "Hide a built-in or Connect IQ activity, glance or app from the watch's lists, or show it again, by queuing an app-list change (IN.BIN) that the watch applies when unplugged. Several changes can be queued; a copy of the registry is kept and returned as backup.",
		Annotations: writes("Hide or show app", false, false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in hideIn) (hiddenResult, error) {
		backup, err := svc.SetAppHidden(ctx, in.App, in.Hidden)
		if err != nil {
			return hiddenResult{}, err
		}

		return hiddenResult{Hidden: in.Hidden, Backup: backup, Note: "Applied when the USB session ends; ask the user to unplug the watch."}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_remove_app",
		Description: "Remove a Connect IQ app, watch face, widget or data field from the watch: deletes its binary, settings and data files, which cannot be restored from the watch; the watch drops it from its list when unplugged. Built-in apps and the Connect IQ Store app are refused. Confirm with the user first.",
		Annotations: writes("Remove app", true, false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in rmIn) (removedResult, error) {
		removed, err := svc.RemoveApp(ctx, in.App)
		if err != nil {
			return removedResult{}, err
		}

		return removedResult{Removed: removed, Note: "The watch finishes the removal when the USB session ends; ask the user to unplug it."}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_install_update",
		Description: "Download one firmware or data update from Garmin, verify its checksum and write it to the watch. The watch installs it after the USB session ends; firmware updates restart the watch. Ask the user before calling this for primary firmware.",
		Annotations: writes("Install update", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in installIn) (*garmin.InstallResult, error) {
		return svc.InstallSoftwareUpdate(ctx, in.PartNumber)
	}))
}

type syncIn struct {
	Upload   *bool `json:"upload,omitempty" jsonschema:"upload new activities and health files (default true)"`
	Download *bool `json:"download,omitempty" jsonschema:"pull what Garmin Connect queued for the watch (default true)"`
}

type cloudSettingsResult struct {
	Settings []garmin.CloudSetting `json:"settings"`
}

type cloudSetIn struct {
	Changes map[string]any `json:"changes" jsonschema:"keys from cloud_settings with their new values, e.g. {\"timeFormat\":\"time_twenty_four_hr\",\"mountingSide\":\"LEFT\"}"`
}

func addCloudTools(srv *mcp.Server, s *server, cloudClient *gc.Client) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_status",
		Description: "Garmin Connect session state: signed in or not, account name, whether this watch is registered on the account. Works without the watch.",
		Annotations: readOnly("Garmin Connect status", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, *garmin.CloudStatus, error) {
		svc, _ := s.svc.Get(ctx) // the account status is useful without the watch
		return nil, garmin.NewCloud(svc, cloudClient).Status(ctx), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_sync",
		Description: "Sync the watch with Garmin Connect over USB like Garmin Express: upload recorded files not yet sent (duplicates the phone already synced are recognised), then download queued items (settings-change files, Connect IQ app installs/updates, workouts, courses) onto the watch. Can take minutes on the first run.",
		Annotations: writes("Sync with Garmin Connect", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in syncIn) (*garmin.SyncResult, error) {
		up := in.Upload == nil || *in.Upload
		down := in.Download == nil || *in.Download

		return garmin.NewCloud(svc, cloudClient).Sync(ctx, up, down, nil)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_settings",
		Description: "The device settings record Garmin Connect keeps for this watch (time/date format, units, wrist, language, alarms, auto-sync, Connect IQ auto-update). These override the watch's own copy (watch_settings) on every sync.",
		Annotations: readOnly("Garmin Connect settings", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (cloudSettingsResult, error) {
		items, err := garmin.NewCloud(svc, cloudClient).Settings(ctx)
		return cloudSettingsResult{Settings: items}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_set_settings",
		Description: "Change settings in Garmin Connect's record for this watch and fetch the settings file Connect generates into GARMIN/NewFiles (applied when the watch is unplugged). Use this for settings marked managedByConnect so Connect stops reverting them.",
		Annotations: writes("Change Connect settings", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in cloudSetIn) (*garmin.CloudApplyResult, error) {
		return garmin.NewCloud(svc, cloudClient).SetSettings(ctx, in.Changes)
	}))
}

type storeSearchIn struct {
	Query string `json:"query,omitempty" jsonschema:"words to search for; empty lists by type"`
	Type  string `json:"type,omitempty" jsonschema:"watchface, watch-app, widget, datafield or audio-content-provider-app"`
	Sort  string `json:"sort,omitempty" jsonschema:"mostPopular, highestRated, mostRecent or trending (listing only)"`
	Page  int    `json:"page,omitempty" jsonschema:"page number, 0 for the first"`
}

type storeAppIn struct {
	ID string `json:"id" jsonschema:"store app id (UUID) from store_search or from watch_apps"`
}

func addStoreTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "store_search",
		Description: "Search or browse the Connect IQ store for apps compatible with this watch (public store data, no login). Each result says whether it is installed and whether the store has a newer version; its id feeds store_app.",
		Annotations: readOnly("Search Connect IQ store", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in storeSearchIn) (*garmin.StoreView, error) {
		dx, err := svc.DeviceXML(ctx)
		if err != nil {
			return nil, err
		}

		q := ciqstore.StoreQuery{Keywords: in.Query, Type: in.Type, Sort: in.Sort, Page: in.Page, PageSize: storePageSize}

		return svc.BrowseStore(ctx, dx.PartNumber, q)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "store_app",
		Description: "Details of one Connect IQ store app: description, what's new, version, size for this watch, permissions, price, recent reviews, and whether it is installed. Installing goes through Garmin Connect: open storeUrl, press Download while signed in there, then cloud_sync (or let the phone deliver it).",
		Annotations: readOnly("Store app details", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in storeAppIn) (*garmin.StoreDetail, error) {
		dx, err := svc.DeviceXML(ctx)
		if err != nil {
			return nil, err
		}

		return svc.StoreApp(ctx, dx.PartNumber, in.ID)
	}))
}

type actsIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many of the newest activities to return (default 30)"`
}

type actIn struct {
	File   string `json:"file" jsonschema:"activity file name from activities_list"`
	Points int    `json:"points,omitempty" jsonschema:"max track points to include (default 200; a negative value keeps laps only)"`
}

type daysIn struct {
	Days int `json:"days,omitempty" jsonschema:"days to return, today included (default 14)"`
}

type weightIn struct {
	Kg float64 `json:"kg" jsonschema:"body weight in kilograms"`
}

type dayIn struct {
	Date string `json:"date" jsonschema:"YYYY-MM-DD"`
}

type daysResult struct {
	Days []garmin.DailyHealth `json:"days"`
}

type weightResult struct {
	OK bool    `json:"ok"`
	Kg float64 `json:"kg"`
}

func addHealthTools(srv *mcp.Server, s *server, cloudClient *gc.Client) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "activities_list",
		Description: "Activities recorded on the watch, decoded from its own FIT files (sport, start, duration, distance, calories, heart rate). The first call decodes files in the background; call again while pending > 0. The file field feeds activity_detail.",
		Annotations: readOnly("List activities", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in actsIn) (*garmin.ActivityList, error) {
		list, err := svc.Activities(ctx, false)
		if err != nil {
			return nil, err
		}

		n := in.Limit
		if n <= 0 {
			n = defaultActivities
		}

		if len(list.Activities) > n {
			list.Activities = list.Activities[:n]
		}

		return list, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "activity_detail",
		Description: "One activity with laps and a downsampled track (time, lat/lon, heart rate, speed, altitude).",
		Annotations: readOnly("Activity detail", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in actIn) (*garmin.ActivityDetail, error) {
		n := in.Points
		if n == 0 {
			n = defaultPoints
		}

		d, err := svc.Activity(ctx, in.File, n)
		if err != nil {
			return nil, err
		}

		if in.Points < 0 {
			d.Points = nil
		}

		return d, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_daily",
		Description: "Daily health from Garmin Connect for the last N days (one request per day, so keep N small): steps and goal, resting/min/max heart rate, average stress, body battery high/low, sleep seconds, calories, floors, intensity minutes. For months or a year use health_history. Needs the Garmin Connect sign-in.",
		Annotations: readOnly("Daily health", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in daysIn) (daysResult, error) {
		days, err := garmin.NewCloud(svc, cloudClient).Daily(ctx, in.Days)
		return daysResult{Days: days}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_history",
		Description: "One row per day for up to a year from Garmin Connect (steps, goal, distance, resting heart rate, average stress, body battery low/high, sleep seconds); a few requests for the whole range, cached on disk. Calories are not available in this form; health_daily has them.",
		Annotations: readOnly("Health history", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in daysIn) (daysResult, error) {
		days, err := garmin.NewCloud(svc, cloudClient).History(ctx, in.Days)
		return daysResult{Days: days}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_extras",
		Description: "Fitness metrics from Garmin Connect: VO2 max, fitness age, HRV status, personal records, earned badges and weight history.",
		Annotations: readOnly("Fitness metrics", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (*garmin.HealthExtras, error) {
		return garmin.NewCloud(svc, cloudClient).Extras(ctx)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_log_weight",
		Description: "Record a manual weigh-in (kg, now) in Garmin Connect. Each call adds one entry.",
		Annotations: writes("Log weight", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in weightIn) (weightResult, error) {
		if err := garmin.NewCloud(svc, cloudClient).LogWeight(ctx, in.Kg, time.Time{}); err != nil {
			return weightResult{}, err
		}

		return weightResult{OK: true, Kg: in.Kg}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_day",
		Description: "Intraday health of one day from Garmin Connect: heart rate, stress and body battery series ([unix ms, value]) and the night's sleep stages and score.",
		Annotations: readOnly("Intraday health", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in dayIn) (*garmin.DayDetail, error) {
		return garmin.NewCloud(svc, cloudClient).Day(ctx, in.Date)
	}))
}

// workoutIn mirrors garmin.WorkoutSpec with one level of repeat groups,
// because a schema for a recursive type cannot be inferred.
type workoutIn struct {
	Name        string   `json:"name" jsonschema:"workout name as it will appear on the watch"`
	Sport       string   `json:"sport" jsonschema:"running, cycling, swimming, walking, hiking, strength, cardio, hiit, yoga, pilates or other"`
	Description string   `json:"description,omitempty"`
	Steps       []stepIn `json:"steps" jsonschema:"the steps in order; a step is either one executable step or a repeat group (repeat > 0 with its own steps)"`
}

type stepIn struct {
	leafStepIn
	Repeat int          `json:"repeat,omitempty" jsonschema:"repeat count of a group; then only steps is read"`
	Steps  []leafStepIn `json:"steps,omitempty" jsonschema:"the steps of a repeat group; groups do not nest"`
}

// leafStepIn is one executable step: exactly one end condition and an
// optional target.
type leafStepIn struct {
	Type     string `json:"type,omitempty" jsonschema:"warmup, cooldown, interval, recovery, rest or other"`
	Name     string `json:"name,omitempty" jsonschema:"optional label shown on the watch"`
	Duration string `json:"duration,omitempty" jsonschema:"end after a time: '10:00', '90s', '10m', '1h'"`
	Distance string `json:"distance,omitempty" jsonschema:"end after a distance: '400m', '2km', '1mi'"`
	Calories int    `json:"calories,omitempty" jsonschema:"end after burning this many kcal"`
	Lap      bool   `json:"lap,omitempty" jsonschema:"end with the lap button"`
	Target   string `json:"target,omitempty" jsonschema:"'hr 140-160', 'hr zone 3', 'pace 4:30-4:45' (min/km), 'speed 10-12' (km/h), 'cadence 170-180' or 'power 200-250'"`
}

// spec converts to the garmin type. The json tags match garmin.WorkoutSpec
// by construction, so a JSON round trip is the conversion and a new field
// is added in two places, not three.
func (w workoutIn) spec() (garmin.WorkoutSpec, error) {
	var out garmin.WorkoutSpec

	b, err := json.Marshal(w)
	if err != nil {
		return out, err
	}

	err = json.Unmarshal(b, &out)

	return out, err
}

type wkIn struct {
	Spec  workoutIn `json:"spec" jsonschema:"the workout: name, sport and steps"`
	Local bool      `json:"local,omitempty" jsonschema:"skip Garmin Connect: write the workout FIT straight into GARMIN/NewFiles (works without sign-in; the workout will not appear in Connect)"`
}

type wkSendIn struct {
	ID int64 `json:"id" jsonschema:"workout id from workouts_list"`
}

type workoutsResult struct {
	Workouts []garmin.CloudWorkout `json:"workouts"`
}

type pulledResult struct {
	Pulled []garmin.PulledFile `json:"pulled"`
}

type workoutResult struct {
	Workout   *garmin.CloudWorkout `json:"workout,omitempty"`
	Pulled    []garmin.PulledFile  `json:"pulled,omitempty"`
	SendError string               `json:"sendError,omitempty"`
	Path      string               `json:"path,omitempty"`
	Note      string               `json:"note,omitempty"`
}

func addWorkoutTools(srv *mcp.Server, s *server, cloudClient *gc.Client) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "workouts_list",
		Description: "Structured workouts stored in Garmin Connect (id, name, sport, estimated duration). The id feeds workout_send.",
		Annotations: readOnly("List workouts", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (workoutsResult, error) {
		list, err := garmin.NewCloud(svc, cloudClient).Workouts(ctx, 0) // default page
		return workoutsResult{Workouts: list}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "workout_create",
		Description: "Create a structured workout from a spec and put it on the watch. By default it is saved in Garmin Connect and queued for the watch; with local=true the FIT file is written directly. The watch shows it under Training > Workouts after the USB session ends.",
		Annotations: writes("Create workout", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in wkIn) (workoutResult, error) {
		spec, err := in.Spec.spec()
		if err != nil {
			return workoutResult{}, err
		}

		if in.Local {
			p, err := svc.WriteWorkoutToWatch(ctx, &spec)
			if err != nil {
				return workoutResult{}, err
			}

			return workoutResult{Path: p, Note: "written to GARMIN/NewFiles; unplug the watch to see it"}, nil
		}

		cloud := garmin.NewCloud(svc, cloudClient)

		wk, err := cloud.CreateWorkout(ctx, &spec)
		if err != nil {
			return workoutResult{}, err
		}

		out := workoutResult{Workout: wk}

		pulled, err := cloud.SendWorkout(ctx, wk.ID, wk.Name)
		if err != nil {
			out.SendError = err.Error()
		} else {
			out.Pulled = pulled
		}

		return out, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "workout_send",
		Description: "Queue an existing Garmin Connect workout for the watch and pull it over USB.",
		Annotations: writes("Send workout to watch", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in wkSendIn) (pulledResult, error) {
		pulled, err := garmin.NewCloud(svc, cloudClient).SendWorkout(ctx, in.ID, "")
		return pulledResult{Pulled: pulled}, err
	}))
}

type plIn struct {
	Name   string   `json:"name" jsonschema:"playlist name"`
	Tracks []string `json:"tracks" jsonschema:"watch paths from music_list, in order"`
}

func addMusicTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "music_list",
		Description: "Audio files and playlists on the watch (Music, Podcasts, Audiobooks), plus streaming provider apps such as YouTube Music whose DRM downloads sit in a hidden cache and are reported by size only. Track paths feed music_playlist.",
		Annotations: readOnly("List music", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (*garmin.MusicLibrary, error) {
		return svc.Music(ctx)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "music_playlist",
		Description: "Write a playlist (Music/<name>.m3u) from tracks already on the watch; an existing playlist of that name is replaced.",
		Annotations: writes("Write playlist", false, false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in plIn) (*garmin.Playlist, error) {
		return svc.WritePlaylist(ctx, in.Name, in.Tracks)
	}))
}
