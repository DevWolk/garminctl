// Package mcp adapts Connect Actions to typed MCP tools.
package mcp

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tools borrows the same account, coordinator and Manager as HTTP/auto.
type Tools struct {
	Manager     *watchsession.Manager
	Coordinator *connect.SyncCoordinator
	CloudFor    func(*watchsession.Session) *connect.Cloud
	JobFor      func(watchsession.ConnectionEpoch) *connect.Cloud
	BindContext func(context.Context) (context.Context, context.CancelFunc)
}
type empty struct{}

type syncIn struct {
	Upload   *bool `json:"upload,omitempty" jsonschema:"upload new activities and health files (default true)"`
	Download *bool `json:"download,omitempty" jsonschema:"pull what Garmin Connect queued for the watch (default true)"`
}

type cloudSettingsResult struct {
	Settings []connect.CloudSetting `json:"settings"`
}

type cloudSetIn struct {
	Changes map[string]any `json:"changes" jsonschema:"keys from cloud_settings with their new values, e.g. {\"timeFormat\":\"time_twenty_four_hr\",\"mountingSide\":\"LEFT\"}"`
}

// RegisterTools preserves four typed tool schemas and annotations.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_status",
		Description: "Read Garmin Connect sign-in and account status, plus watch registration when connected. Works without a watch.",
		Annotations: mcpx.ReadOnly("Garmin Connect status", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, *connect.CloudStatus, error) {
		ctx, cancel := t.BindContext(ctx)
		defer cancel()

		lease, err := t.Manager.Acquire(ctx)
		if err != nil {
			release, admissionErr := t.Manager.Gate().Enter(ctx)
			if admissionErr != nil {
				return nil, nil, admissionErr
			}
			defer release()

			return nil, (connect.StatusAction{Cloud: t.CloudFor(nil)}).Run(ctx), nil
		}

		out := (connect.StatusAction{Cloud: t.CloudFor(lease.Session())}).Run(lease.Context())

		return nil, out, lease.Release()
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_sync",
		Description: "Upload recorded files to Garmin Connect, then download queued apps, settings, workouts and courses to the watch. Recognizes duplicate uploads.",
		Annotations: mcpx.Writes("Sync with Garmin Connect", false, true),
	}, t.syncTool())

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_settings",
		Description: "Read watch settings saved in Garmin Connect. Use cloud_set_settings to keep Connect's values consistent with local changes.",
		Annotations: mcpx.ReadOnly("Garmin Connect settings", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, _ empty) (cloudSettingsResult, error) {
		items, err := (connect.SettingsAction{Cloud: t.CloudFor(lease.Session())}).Run(ctx)
		return cloudSettingsResult{Settings: items}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cloud_set_settings",
		Description: "Save Garmin Connect settings and fetch any settings file it queues. Unplug to apply downloaded changes. Use for fields marked managedByConnect.",
		Annotations: mcpx.Writes("Change Connect settings", false, true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in cloudSetIn) (*connect.CloudApplyResult, error) {
		return (connect.ApplySettingsAction{Cloud: t.CloudFor(lease.Session())}).Run(ctx, in.Changes)
	}))
}

func (t *Tools) syncTool() mcp.ToolHandlerFor[syncIn, *connect.SyncResult] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in syncIn) (*mcp.CallToolResult, *connect.SyncResult, error) {
		ctx, cancel := t.BindContext(ctx)
		defer cancel()

		lease, err := t.Manager.Acquire(ctx)
		if err != nil {
			return nil, nil, err
		}

		epoch := lease.Epoch()
		if err := lease.Release(); err != nil {
			return nil, nil, err
		}

		up, down := in.Upload == nil || *in.Upload, in.Download == nil || *in.Download
		cloud := t.JobFor(epoch)

		job, _, err := t.Coordinator.JoinOrStart(ctx, connect.SyncOptions{Upload: up, Download: down, Epoch: epoch}, func(ctx context.Context, progress connect.Progress) (*connect.SyncResult, error) {
			return (connect.SyncAction{Cloud: cloud}).Run(ctx, up, down, progress)
		})
		if err != nil {
			return nil, nil, err
		}

		out, err := job.Wait(ctx)

		return nil, out, err
	}
}
