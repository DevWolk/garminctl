// Package mcp adapts watch update Actions to typed tools.
package mcp

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tools uses the runtime's existing watch and current feature cache callbacks.
type Tools struct {
	Manager       *watchsession.Manager
	Config        func() updates.Config
	Registry      func(*watchsession.Session) updates.InstalledApps
	InvalidateFor func(*watchsession.Session) func(uint64)
	BindContext   func(context.Context) (context.Context, context.CancelFunc)
}
type (
	empty     struct{}
	installIn struct {
		PartNumber string `json:"partNumber" jsonschema:"part number returned by watch_check_updates"`
	}
)

type updatesResult struct {
	Software      []updates.SoftwareUpdate `json:"software"`
	Apps          []updates.AppUpdate      `json:"apps"`
	SoftwareError string                   `json:"softwareError,omitempty"`
	AppsError     string                   `json:"appsError,omitempty"`
}

// RegisterTools retains both update tools and their exact schemas/annotations.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_check_updates",
		Description: "Check for watch software and Connect IQ app updates. Installs nothing. Use a software result's partNumber with watch_install_update.",
		Annotations: mcpx.ReadOnly("Check for updates", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, _ empty) (updatesResult, error) {
		sw, swErr := updates.NewCheckSoftwareAction(t.Config()).Run(ctx, lease.Session())
		apps, appErr := updates.NewCheckAppsAction(t.Registry(lease.Session())).Run(ctx)

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
		Name:        "watch_install_update",
		Description: "Download and verify one software update, then write it to the watch. Unplug to install. Primary firmware restarts the watch; ask the user before installing it.",
		Annotations: mcpx.Writes("Install update", false, true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in installIn) (*updates.InstallResult, error) {
		return updates.NewInstallSoftwareAction(t.Config(), t.InvalidateFor(lease.Session())).Run(ctx, lease.Session(), in.PartNumber)
	}))
}
