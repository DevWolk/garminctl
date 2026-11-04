// Package mcpserver exposes watch and Garmin Connect tools over MCP.
package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
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
