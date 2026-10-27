// Package mcpserver exposes watch and Garmin Connect tools over MCP.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
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

const instructions = `Manage a Garmin watch over USB and access Garmin Connect after sign-in.

Use watch_settings_schema before changing watch settings. Address fields by message and field name
(deviceSettings.backlightMode) or number. watch_set_settings queues changes for the next USB disconnect;
tell the user to unplug the watch. App settings use watch_app_settings and watch_set_app_settings,
which save directly to the watch.
Use cloud_set_settings for fields marked managedByConnect so future Connect syncs use the same values.
Sign in through the web UI or "garminctl cloud login"; never ask for a password in this conversation.
cloud_sync uploads recorded files and downloads queued settings, apps, workouts and courses.
watch_install_update verifies downloads and writes them to the watch for installation on unplug.
Primary firmware updates restart the watch; ask the user before installing them. Confirm app removal first.
App installations and updates can arrive through the Connect queue or a .PRG file uploaded in the web UI.

Use IDs returned by the tools: watch_apps for app tools; watch_check_updates software partNumber for
watch_install_update; activities_list.file for activity_detail; workouts_list.id for workout_send;
music_list track paths for music_playlist; store_search.id for store_app.`

// withWatch adapts a tool body that needs the connected watch.
func withWatch[In, Out any](s *server, body func(ctx context.Context, svc *garmin.Service, in In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		svc, err := s.svc.Get(ctx)
		if err != nil {
			var zero Out

			return nil, zero, err
		}

		out, err := body(ctx, svc, in) // the SDK turns a non-nil err into an error result and drops out
		if errors.Is(err, device.ErrDisconnected) {
			s.svc.Drop()
		}

		return nil, out, err
	}
}

// readOnly marks tools that leave watch and account data unchanged.
// network marks access to Garmin's services.
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
	Changes []garmin.Change `json:"changes" jsonschema:"field changes: mesg and field (name or number), value (enum name, number or scaled value), optional index for repeated messages and arrayIndex for array fields"`
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
		Description: "Read the connected watch's model, unit ID, software, app count, pending files and storage.",
		Annotations: readOnly("Watch summary", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (*garmin.Summary, error) {
		return svc.Summary(ctx)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_list_files",
		Description: "List watch files with names, sizes and modification times. Use their paths with watch_decode_fit or watch_delete_file.",
		Annotations: readOnly("List watch files", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in listIn) (filesResult, error) {
		entries, err := svc.Device().List(ctx, in.Path)
		return filesResult{Entries: entries}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_decode_fit",
		Description: "Decode a watch FIT file into named messages and fields. Undocumented fields use numbers. Returns up to 400 messages.",
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
		Description: "Permanently delete one watch file, including a pending file in GARMIN/NewFiles. There is no undo. Use watch_remove_app for apps.",
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
		Description: "Read watch settings with values, types, options and notes. Use watch_app_settings for Connect IQ apps and cloud_settings for Garmin Connect's copy.",
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
		Description: "List documented settings fields, types, units and options. Use before watch_set_settings.",
		Annotations: readOnly("Settings field schema", false),
	}, withWatch(s, func(_ context.Context, svc *garmin.Service, in schemaIn) (fieldsResult, error) {
		fields, err := svc.Schema(in.Message)
		return fieldsResult{Fields: fields}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_set_settings",
		Description: "Queue watch settings changes and return their previous values. Tell the user to unplug the watch to apply them.",
		Annotations: writes("Change watch settings", false, false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in setIn) (*garmin.ApplyResult, error) {
		return svc.ApplyChanges(ctx, in.Changes)
	}))
}

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
	App string `json:"app" jsonschema:"app name, file name or store ID from watch_apps"`
}

type appSchemaIn struct {
	App string `json:"app" jsonschema:"app name, file name or store ID from watch_apps"`
	Key string `json:"key,omitempty" jsonschema:"restrict to one setting key; omit for all"`
}

type appSetIn struct {
	App    string         `json:"app" jsonschema:"app name, file name or store ID from watch_apps"`
	Values map[string]any `json:"values" jsonschema:"keys to change with their new values; for dropdowns pass the option label (e.g. \"Body Battery %\") or its value, for toggles on/off, colors as RRGGBB hex strings where the schema says so"`
}

type installIn struct {
	PartNumber string `json:"partNumber" jsonschema:"part number returned by watch_check_updates"`
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
		Description: "List installed apps, watch faces, glances and activities, including visibility. Use a Connect IQ app's fileName with the app settings tools.",
		Annotations: readOnly("List watch apps", false),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (appsResult, error) {
		apps, err := svc.Apps(ctx)
		return appsResult{Apps: apps}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_app_settings",
		Description: "Read Connect IQ app or watch face settings, including labels and types when available. Use watch_app_settings_schema for options.",
		Annotations: readOnly("Read app settings", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in appIn) (*garmin.AppSettings, error) {
		return svc.AppSettings(ctx, in.App)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_app_settings_schema",
		Description: "Read an app's setting labels, options, ranges and read-only markers from the store. Pass key to return one setting.",
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
		Description: "Save Connect IQ app or watch face settings directly to the watch. Validates against the store schema when available; option labels are accepted.",
		Annotations: writes("Change app settings", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in appSetIn) (*garmin.AppSettings, error) {
		return svc.SetAppSettings(ctx, in.App, in.Values)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_check_updates",
		Description: "Check for watch software and Connect IQ app updates. Installs nothing. Use a software result's partNumber with watch_install_update.",
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
		Description: "Queue visibility changes for a built-in or Connect IQ activity, glance or app. Applies on unplug. Includes a registry backup path when available.",
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
		Description: "Permanently delete an app, its settings and data. Unplug to finish removal. Built-in apps and the Connect IQ Store app are protected. Confirm with the user first.",
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
		Description: "Download and verify one software update, then write it to the watch. Unplug to install. Primary firmware restarts the watch; ask the user before installing it.",
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
		Description: "Read Garmin Connect sign-in and account status, plus watch registration when connected. Works without a watch.",
		Annotations: readOnly("Garmin Connect status", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, *garmin.CloudStatus, error) {
		svc, _ := s.svc.Get(ctx) // the account status is useful without the watch
		return nil, garmin.NewCloud(svc, cloudClient).Status(ctx), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_sync",
		Description: "Upload recorded files to Garmin Connect, then download queued apps, settings, workouts and courses to the watch. Recognizes duplicate uploads.",
		Annotations: writes("Sync with Garmin Connect", false, true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in syncIn) (*garmin.SyncResult, error) {
		up := in.Upload == nil || *in.Upload
		down := in.Download == nil || *in.Download

		return garmin.NewCloud(svc, cloudClient).Sync(ctx, up, down, nil)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_settings",
		Description: "Read watch settings saved in Garmin Connect. Use cloud_set_settings to keep Connect's values consistent with local changes.",
		Annotations: readOnly("Garmin Connect settings", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (cloudSettingsResult, error) {
		items, err := garmin.NewCloud(svc, cloudClient).Settings(ctx)
		return cloudSettingsResult{Settings: items}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_set_settings",
		Description: "Save Garmin Connect settings and fetch any settings file it queues. Unplug to apply downloaded changes. Use for fields marked managedByConnect.",
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
		Description: "Find compatible Connect IQ apps with install and update status. No sign-in needed. Use a result's id with store_app.",
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
		Description: "Read app details, version, size, permissions, price, reviews and install status. Use the Connect IQ phone app to install; cloud_sync downloads queued files.",
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
	Days int `json:"days,omitempty" jsonschema:"days including today; default 14 for health_daily, 30 for health_history"`
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
		Description: "List recorded activities with time, sport, distance, calories and heart rate. Call again while pending > 0. Use file with activity_detail.",
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
		Description: "Read daily health from Garmin Connect, including steps, heart rate, stress, sleep and calories. Makes one request per day; use health_history for longer ranges. Sign-in required.",
		Annotations: readOnly("Daily health", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in daysIn) (daysResult, error) {
		days, err := garmin.NewCloud(svc, cloudClient).Daily(ctx, in.Days)
		return daysResult{Days: days}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_history",
		Description: "Read up to a year of daily steps, distance, resting heart rate, stress, body battery and sleep from Garmin Connect. Excludes calories and intensity minutes. Sign-in required.",
		Annotations: readOnly("Health history", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, in daysIn) (daysResult, error) {
		days, err := garmin.NewCloud(svc, cloudClient).History(ctx, in.Days)
		return daysResult{Days: days}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_extras",
		Description: "Read VO2 max, fitness age, HRV, records, badges and weight history from Garmin Connect. Sign-in required.",
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
		Description: "Read one day's heart rate, stress and body battery series ([Unix ms, value]), sleep stages and sleep score from Garmin Connect. Sign-in required.",
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

// spec converts matching JSON fields without duplicating the field mapping.
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
		Description: "List Garmin Connect workouts with id, name, sport and estimated duration. Use id with workout_send.",
		Annotations: readOnly("List workouts", true),
	}, withWatch(s, func(ctx context.Context, svc *garmin.Service, _ empty) (workoutsResult, error) {
		list, err := garmin.NewCloud(svc, cloudClient).Workouts(ctx, 0) // default page
		return workoutsResult{Workouts: list}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "workout_create",
		Description: "Create a workout in Garmin Connect and attempt USB delivery. With local=true, save only to the watch. Unplug after delivery to load the workout.",
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
		Description: "Queue a Garmin Connect workout and attempt USB delivery. Unplug after delivery to load it; sync later if no file is returned.",
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
		Description: "List audio files, playlists and streaming app cache sizes. Streaming downloads are excluded. Use track paths with music_playlist.",
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
