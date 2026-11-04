// Package mcpserver exposes watch and Garmin Connect tools over MCP.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
)

const (
	defaultActivities = 30
	defaultPoints     = 200
)

// server holds the lazily connected watch the tools share.
type server struct {
	svc     *garmin.Lazy
	runtime *garmin.Runtime
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
	return mcpx.WithWatch(s.svc, s.requestContext, func(ctx context.Context, lease *garmin.Lease, in In) (Out, error) {
		return body(ctx, lease.Service(), in)
	})
}

func (s *server) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.runtime != nil {
		return s.runtime.RequestContext(ctx)
	}

	return ctx, func() {}
}

// readOnly marks tools that leave watch and account data unchanged.
// network marks access to Garmin's services.
func readOnly(title string, network bool) *mcp.ToolAnnotations {
	return mcpx.ReadOnly(title, network)
}

// writes marks a tool that changes the watch or the Garmin account.
func writes(title string, destructive, network bool) *mcp.ToolAnnotations {
	return mcpx.Writes(title, destructive, network)
}

// ServeRuntime runs the stdio shell with process owners supplied by app wiring.
// It closes the supplied runtime before returning.
func ServeRuntime(runtime *garmin.Runtime, account *gc.Client, version string, registrars ...mcpx.ToolRegistrar) (err error) {
	s := &server{svc: runtime.Watch, runtime: runtime}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(runtime.Context()), 3*time.Second)
		defer cancel()

		err = errors.Join(err, runtime.Close(closeCtx))
	}()

	srv := newServer(s, version, account, registrars...)

	return srv.Run(runtime.Context(), &mcp.StdioTransport{})
}

func newServer(s *server, version string, cloudClient *gc.Client, registrars ...mcpx.ToolRegistrar) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "garminctl", Version: version}, &mcp.ServerOptions{Instructions: instructions})

	for _, registrar := range registrars {
		registrar.RegisterTools(srv)
	}

	addCloudTools(srv, s, cloudClient)
	addHealthTools(srv, s, cloudClient)
	addWorkoutTools(srv, s, cloudClient)
	addMusicTools(srv, s)

	return srv
}

type empty struct{}

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
		ctx, cancel := s.runtime.RequestContext(ctx)
		defer cancel()

		lease, err := s.svc.Acquire(ctx)
		if err != nil {
			release, admissionErr := s.svc.Gate().Enter(ctx)
			if admissionErr != nil {
				return nil, nil, admissionErr
			}
			defer release()

			return nil, garmin.NewCloud(nil, cloudClient).Status(ctx), nil
		}

		out := garmin.NewCloud(lease.Service(), cloudClient).Status(lease.Context())

		return nil, out, lease.Release()
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_sync",
		Description: "Upload recorded files to Garmin Connect, then download queued apps, settings, workouts and courses to the watch. Recognizes duplicate uploads.",
		Annotations: writes("Sync with Garmin Connect", false, true),
	}, s.syncTool(cloudClient))

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
	}, s.activitiesTool())

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

func (s *server) syncTool(client *gc.Client) mcp.ToolHandlerFor[syncIn, *garmin.SyncResult] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in syncIn) (*mcp.CallToolResult, *garmin.SyncResult, error) {
		ctx, cancel := s.runtime.RequestContext(ctx)
		defer cancel()

		lease, err := s.svc.Acquire(ctx)
		if err != nil {
			return nil, nil, err
		}

		epoch := lease.Epoch()
		if err := lease.Release(); err != nil {
			return nil, nil, err
		}

		up, down := in.Upload == nil || *in.Upload, in.Download == nil || *in.Download
		cloud := garmin.NewJobCloud(s.svc, epoch, client)

		job, _, err := s.runtime.Sync.JoinOrStart(ctx, garmin.SyncOptions{Upload: up, Download: down, Epoch: epoch}, func(ctx context.Context, progress garmin.Progress) (*garmin.SyncResult, error) {
			return cloud.Sync(ctx, up, down, progress)
		})
		if err != nil {
			return nil, nil, err
		}

		out, err := job.Wait(ctx)

		return nil, out, err
	}
}

func (s *server) activitiesTool() mcp.ToolHandlerFor[actsIn, *garmin.ActivityList] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in actsIn) (*mcp.CallToolResult, *garmin.ActivityList, error) {
		ctx, cancel := s.runtime.RequestContext(ctx)
		defer cancel()

		list, err := s.runtime.Activities.Activities(ctx, false)
		if err != nil {
			return nil, nil, err
		}

		n := in.Limit
		if n <= 0 {
			n = defaultActivities
		}

		if len(list.Activities) > n {
			list.Activities = list.Activities[:n]
		}

		return nil, list, nil
	}
}
