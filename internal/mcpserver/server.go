// Package mcpserver exposes the watch to AI assistants over MCP (stdio).
package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"garminctl/internal/ciqstore"
	"garminctl/internal/device"
	"garminctl/internal/garmin"
	"garminctl/internal/gc"
)

// Opener connects to the device on demand.
type Opener func(ctx context.Context) (device.Device, error)

type server struct {
	open Opener
	mu   sync.Mutex
	svc  *garmin.Service
}

func (s *server) service(ctx context.Context) (*garmin.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc != nil {
		return s.svc, nil
	}
	octx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dev, err := s.open(octx)
	if err != nil {
		return nil, err
	}
	s.svc = garmin.New(dev)
	return s.svc, nil
}

func (s *server) drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc != nil {
		_ = s.svc.Close()
		s.svc = nil
	}
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
to stop Connect reverting a watch setting).`

// Serve runs the MCP server until ctx ends.
func Serve(ctx context.Context, open Opener) error {
	s := &server{open: open}
	defer s.drop()
	srv := mcp.NewServer(&mcp.Implementation{Name: "garminctl", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: instructions})

	type empty struct{}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_info", Description: "Summary of the connected watch: model, unit id, firmware, installed app count, pending NewFiles."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.Summary(ctx))
		})

	type listIn struct {
		Path string `json:"path" jsonschema:"directory on the watch, e.g. GARMIN/Activity; empty for the storage root"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_list_files", Description: "List a directory on the watch (names, sizes, modification times)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.Device().List(ctx, in.Path))
		})

	type pathIn struct {
		Path string `json:"path" jsonschema:"file path on the watch, e.g. GARMIN/Sports/WalkingWalk.fit"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_decode_fit", Description: "Decode any FIT file on the watch (settings, sport profiles, goals, activities) into named messages and fields. Undocumented fields are reported by number."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			msgs, err := svc.DecodeFIT(ctx, in.Path)
			if err != nil {
				return nil, nil, err
			}
			if len(msgs) > 400 {
				msgs = msgs[:400]
			}
			return nil, msgs, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "watch_delete_file", Description: "Delete a file on the watch. Use with care; mainly for removing pending files from GARMIN/NewFiles."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.Device().Delete(ctx, in.Path); err != nil {
				return nil, nil, err
			}
			svc.Refresh()
			return nil, map[string]any{"deleted": in.Path}, nil
		})

	type settingsIn struct {
		IncludeUnknown bool   `json:"includeUnknown,omitempty" jsonschema:"also return undocumented fields (numbers only)"`
		Message        string `json:"message,omitempty" jsonschema:"restrict to one message, e.g. deviceSettings or userProfile"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_settings", Description: "Read the watch settings (GARMIN/Settings/Settings.fit): device settings, user profile, HR zones, alarms, sensors and more. Returns one row per field with value, type, enum options and notes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in settingsIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			view, err := svc.Settings(ctx)
			if err != nil {
				return nil, nil, err
			}
			var out []garmin.SettingsRow
			for _, r := range view.Rows() {
				if !in.IncludeUnknown && !r.Known && r.Note == "" {
					continue
				}
				if in.Message != "" && r.Mesg != in.Message && fmt.Sprint(r.MesgNum) != in.Message {
					continue
				}
				out = append(out, r)
			}
			return nil, out, nil
		})

	type schemaIn struct {
		Message string `json:"message" jsonschema:"message name or number, e.g. deviceSettings, userProfile, hrZone, zonesTarget"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_settings_schema", Description: "List the documented fields of a FIT settings message with types, units and enum options. Use before watch_set_settings."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in schemaIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.Schema(in.Message))
		})

	type setIn struct {
		Changes []garmin.Change `json:"changes" jsonschema:"field changes; each has mesg (name or number), field (name or number), value (enum name, number or scaled value such as 79.5 for weight), optional index for repeated messages and arrayIndex for array fields"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_set_settings", Description: "Change watch settings. Writes a Garmin settings-change file into GARMIN/NewFiles; the watch applies it when the USB session ends, so tell the user to unplug or eject the watch afterwards. Returns the resolved changes with previous values."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in setIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.ApplyChanges(ctx, in.Changes))
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "watch_apps", Description: "List installed apps, watch faces, glances and activities. Connect IQ apps have a fileName (e.g. G5RG4154 for Easy+) used by the app settings tools."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.Apps(ctx))
		})

	type appIn struct {
		App string `json:"app" jsonschema:"app name (Easy+), file name (G5RG4154) or store id"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_app_settings", Description: "Read a Connect IQ app's or watch face's settings: for each key the human label, type (select/toggle/text/number), current value and, for dropdowns, the label of the current choice. Use watch_app_settings_schema to see the available dropdown options."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in appIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.AppSettings(ctx, in.App))
		})

	type appSchemaIn struct {
		App string `json:"app" jsonschema:"app name (Easy+), file name (G5RG4154) or store id"`
		Key string `json:"key,omitempty" jsonschema:"restrict to one setting key; omit for all"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_app_settings_schema", Description: "Settings definition of a Connect IQ app as Garmin's store publishes it: labels, dropdown options with their values, number ranges, read-only markers. Large for apps with many dropdowns; pass key to narrow."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in appSchemaIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			sc, err := svc.AppSchema(ctx, in.App)
			if err != nil {
				return nil, nil, err
			}
			if in.Key == "" {
				return nil, sc, nil
			}
			st, ok := sc.Get(in.Key)
			if !ok {
				return nil, nil, fmt.Errorf("no setting %q", in.Key)
			}
			return nil, st, nil
		})

	type appSetIn struct {
		App    string         `json:"app" jsonschema:"app name (Easy+), file name (G5RG4154) or store id"`
		Values map[string]any `json:"values" jsonschema:"keys to change with their new values; for dropdowns pass the option label (e.g. \"Body Battery %\") or its value, for toggles on/off, colors as RRGGBB hex strings where the schema says so"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_set_app_settings", Description: "Change Connect IQ app or watch-face settings and write the .SET file back to the watch. Values are validated against the store's settings definition (dropdown labels are accepted). Takes effect immediately."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in appSetIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.SetAppSettings(ctx, in.App, in.Values))
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "watch_check_updates", Description: "Check Garmin's update service for firmware and data updates for this watch (the same service Garmin Express uses) and compare installed Connect IQ apps with the store. Returns both lists; nothing is installed."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			sw, swErr := svc.CheckSoftwareUpdates(ctx)
			apps, appErr := svc.CheckAppUpdates(ctx)
			out := map[string]any{"software": sw, "apps": apps}
			if swErr != nil {
				out["softwareError"] = swErr.Error()
			}
			if appErr != nil {
				out["appsError"] = appErr.Error()
			}
			return nil, out, nil
		})

	type installIn struct {
		PartNumber string `json:"partNumber" jsonschema:"part number of an update returned by watch_check_updates, e.g. 006-B4426-00"`
	}
	type rmIn struct {
		App string `json:"app" jsonschema:"the app's name, file id or store id from watch_apps"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "watch_remove_app", Description: "Remove a Connect IQ app, watch face, widget or data field from the watch: deletes its binary, settings and data files; the watch drops it from its list when unplugged. Built-in apps and the Connect IQ Store app are refused. Confirm with the user first."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in rmIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			removed, err := svc.RemoveApp(ctx, in.App)
			if err != nil {
				return nil, nil, err
			}
			return must(map[string]any{"removed": removed, "note": "The watch finishes the removal when the USB session ends; ask the user to unplug it."}, nil)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "watch_install_update", Description: "Download one firmware or data update from Garmin, verify its checksum and write it to the watch. The watch installs it after the USB session ends; firmware updates restart the watch. Ask the user before calling this for primary firmware."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in installIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.InstallSoftwareUpdate(ctx, in.PartNumber))
		})

	cloudClient := gc.New(gc.DefaultStorePath())
	mcp.AddTool(srv, &mcp.Tool{Name: "cloud_status", Description: "Garmin Connect session state: signed in or not, account name, whether this watch is registered on the account."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			svc, _ := s.service(ctx)
			return nil, garmin.NewCloud(svc, cloudClient).Status(ctx), nil
		})

	type syncIn struct {
		Upload   *bool `json:"upload,omitempty" jsonschema:"upload new activities and health files (default true)"`
		Download *bool `json:"download,omitempty" jsonschema:"pull what Garmin Connect queued for the watch (default true)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "cloud_sync", Description: "Sync the watch with Garmin Connect over USB like Garmin Express: upload recorded files not yet sent (duplicates the phone already synced are recognised), then download queued items (settings-change files, Connect IQ app installs/updates, workouts, courses) onto the watch. Can take minutes on the first run."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in syncIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			up := in.Upload == nil || *in.Upload
			down := in.Download == nil || *in.Download
			return must(garmin.NewCloud(svc, cloudClient).Sync(ctx, up, down, nil))
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "cloud_settings", Description: "The device settings record Garmin Connect keeps for this watch (time/date format, units, wrist, language, alarms, auto-sync, Connect IQ auto-update). These override the watch's own copy on every sync."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(garmin.NewCloud(svc, cloudClient).Settings(ctx))
		})

	type cloudSetIn struct {
		Changes map[string]any `json:"changes" jsonschema:"keys from cloud_settings with their new values, e.g. {\"timeFormat\":\"time_twenty_four_hr\",\"mountingSide\":\"LEFT\"}"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "cloud_set_settings", Description: "Change settings in Garmin Connect's record for this watch and fetch the settings file Connect generates into GARMIN/NewFiles (applied when the watch is unplugged). Use this for settings marked managedByConnect so Connect stops reverting them."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in cloudSetIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(garmin.NewCloud(svc, cloudClient).SetSettings(ctx, in.Changes))
		})

	type storeSearchIn struct {
		Query string `json:"query,omitempty" jsonschema:"words to search for; empty lists by type"`
		Type  string `json:"type,omitempty" jsonschema:"watchface, watch-app, widget, datafield or audio-content-provider-app"`
		Sort  string `json:"sort,omitempty" jsonschema:"mostPopular, highestRated, mostRecent or trending (listing only)"`
		Page  int    `json:"page,omitempty"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "store_search", Description: "Search or browse the Connect IQ store for apps compatible with this watch (public store data, no login). Each result says whether it is installed and whether the store has a newer version."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in storeSearchIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			dx, err := svc.DeviceXML(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.BrowseStore(ctx, dx.PartNumber, ciqstore.StoreQuery{Keywords: in.Query, Type: in.Type, Sort: in.Sort, Page: in.Page, PageSize: 20}))
		})

	type storeAppIn struct {
		ID string `json:"id" jsonschema:"store app id (UUID)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "store_app", Description: "Details of one Connect IQ store app: description, what's new, version, size for this watch, permissions, price, recent reviews, and whether it is installed. Installing goes through Garmin Connect: open storeUrl, press Download while signed in there, then cloud_sync (or let the phone deliver it)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in storeAppIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			dx, err := svc.DeviceXML(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.StoreApp(ctx, dx.PartNumber, in.ID))
		})

	type actsIn struct {
		Limit int `json:"limit,omitempty" jsonschema:"how many of the newest activities to return (default 30)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "activities_list", Description: "Activities recorded on the watch, decoded from its own FIT files (sport, start, duration, distance, calories, heart rate). The first call decodes files in the background; call again while pending > 0."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in actsIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			list, err := svc.Activities(ctx, false)
			if err != nil {
				return nil, nil, err
			}
			n := in.Limit
			if n <= 0 {
				n = 30
			}
			if len(list.Activities) > n {
				list.Activities = list.Activities[:n]
			}
			return nil, list, nil
		})

	type actIn struct {
		File   string `json:"file" jsonschema:"activity file name from activities_list"`
		Points int    `json:"points,omitempty" jsonschema:"max track points to include (default 200; 0 keeps laps only)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "activity_detail", Description: "One activity with laps and a downsampled track (time, lat/lon, heart rate, speed, altitude)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in actIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			n := in.Points
			if n == 0 {
				n = 200
			}
			d, err := svc.Activity(ctx, in.File, n)
			if err != nil {
				return nil, nil, err
			}
			if in.Points < 0 {
				d.Points = nil
			}
			return nil, d, nil
		})

	type daysIn struct {
		Days int `json:"days,omitempty" jsonschema:"days to return, today included (default 14, max 90)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "health_daily", Description: "Daily health from Garmin Connect for the last N days: steps and goal, resting/min/max heart rate, average stress, body battery high/low, sleep seconds, calories, floors, intensity minutes. Needs the Garmin Connect sign-in."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in daysIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(garmin.NewCloud(svc, cloudClient).Daily(ctx, in.Days))
		})

	type dayIn struct {
		Date string `json:"date" jsonschema:"YYYY-MM-DD"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "health_day", Description: "Intraday health of one day from Garmin Connect: heart rate, stress and body battery series ([unix ms, value]) and the night's sleep stages and score."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in dayIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(garmin.NewCloud(svc, cloudClient).Day(ctx, in.Date))
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "workouts_list", Description: "Structured workouts stored in Garmin Connect (id, name, sport, estimated duration)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(garmin.NewCloud(svc, cloudClient).Workouts(ctx, 50))
		})

	type wkIn struct {
		Spec  garmin.WorkoutSpec `json:"spec" jsonschema:"the workout: name, sport (running, cycling, swimming, walking, hiking, strength, cardio, hiit, yoga, pilates, other) and steps. A step has type (warmup, cooldown, interval, recovery, rest, other), exactly one end condition (duration like '10:00' or '90s', distance like '400m' or '2km', calories, or lap=true for the lap button) and an optional target ('hr 140-160', 'hr zone 3', 'pace 4:30-4:45' in min/km, 'speed 10-12' in km/h, 'cadence 170-180', 'power 200-250'). A repeat group is {repeat: N, steps: [...]}."`
		Send  bool               `json:"send,omitempty" jsonschema:"also queue it for the watch through Garmin Connect and pull it onto the watch (default true)"`
		Local bool               `json:"local,omitempty" jsonschema:"skip Garmin Connect: write the workout FIT straight into GARMIN/NewFiles (works without sign-in; the workout will not appear in Connect)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "workout_create", Description: "Create a structured workout from a spec and put it on the watch. By default it is saved in Garmin Connect and queued for the watch; with local=true the FIT file is written directly. The watch shows it under Training > Workouts after the USB session ends."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in wkIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			if in.Local {
				p, err := svc.WriteWorkoutToWatch(ctx, &in.Spec)
				if err != nil {
					return nil, nil, err
				}
				return nil, map[string]any{"path": p, "note": "written to GARMIN/NewFiles; unplug the watch to see it"}, nil
			}
			cloud := garmin.NewCloud(svc, cloudClient)
			wk, err := cloud.CreateWorkout(ctx, &in.Spec)
			if err != nil {
				return nil, nil, err
			}
			out := map[string]any{"workout": wk}
			pulled, err := cloud.SendWorkout(ctx, wk.ID, wk.Name)
			if err != nil {
				out["sendError"] = err.Error()
			} else {
				out["pulled"] = pulled
			}
			return nil, out, nil
		})

	type wkSendIn struct {
		ID int64 `json:"id" jsonschema:"workout id from workouts_list"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "workout_send", Description: "Queue an existing Garmin Connect workout for the watch and pull it over USB."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in wkSendIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(garmin.NewCloud(svc, cloudClient).SendWorkout(ctx, in.ID, ""))
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "music_list", Description: "Audio files and playlists on the watch (Music, Podcasts, Audiobooks), plus streaming provider apps such as YouTube Music whose DRM downloads sit in a hidden cache and are reported by size only."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.Music(ctx))
		})

	type plIn struct {
		Name   string   `json:"name" jsonschema:"playlist name"`
		Tracks []string `json:"tracks" jsonschema:"watch paths from music_list, in order"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "music_playlist", Description: "Write a playlist (Music/<name>.m3u) from tracks already on the watch."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in plIn) (*mcp.CallToolResult, any, error) {
			svc, err := s.service(ctx)
			if err != nil {
				return nil, nil, err
			}
			return must(svc.WritePlaylist(ctx, in.Name, in.Tracks))
		})

	return srv.Run(ctx, &mcp.StdioTransport{})
}

func must[T any](v T, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return nil, nil, err
	}
	return nil, v, nil
}
