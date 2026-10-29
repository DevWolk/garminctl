// Package mcp adapts installed-app Actions to typed MCP tools.
package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Tools uses the backend's Manager and canonical current-epoch schema state.
type Tools struct {
	Manager     *watchsession.Manager
	CacheFor    func(*watchsession.Session) *apps.SchemaCache
	BindContext func(context.Context) (context.Context, context.CancelFunc)
}

type empty struct{}

type appsResult struct {
	Apps []apps.App `json:"apps"`
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

type hideIn struct {
	App    string `json:"app" jsonschema:"the entry's name, file id or store id from watch_apps"`
	Hidden bool   `json:"hidden" jsonschema:"true removes it from the watch's activity list / glance loop / apps, false puts it back"`
}

type rmIn struct {
	App string `json:"app" jsonschema:"the app's name, file id or store id from watch_apps"`
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

// RegisterTools preserves the six apps tools and their schema metadata.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_apps",
		Description: "List installed apps, watch faces, glances and activities, including visibility. Use a Connect IQ app's fileName with the app settings tools.",
		Annotations: mcpx.ReadOnly("List watch apps", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, _ empty) (appsResult, error) {
		apps, err := (apps.ReadRegistryAction{}).Run(ctx, lease.Session())
		return appsResult{Apps: apps}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_app_settings",
		Description: "Read Connect IQ app or watch face settings, including labels and types when available. Use watch_app_settings_schema for options.",
		Annotations: mcpx.ReadOnly("Read app settings", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in appIn) (*apps.AppSettings, error) {
		return apps.NewReadSettingsAction(t.CacheFor(lease.Session())).Run(ctx, lease.Session(), in.App)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_app_settings_schema",
		Description: "Read an app's setting labels, options, ranges and read-only markers from the store. Pass key to return one setting.",
		Annotations: mcpx.ReadOnly("App settings schema", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in appSchemaIn) (*ciqstore.Schema, error) {
		sc, err := apps.NewSchemaAction(t.CacheFor(lease.Session())).Run(ctx, lease.Session(), in.App)
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
		Annotations: mcpx.Writes("Change app settings", false, true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in appSetIn) (*apps.AppSettings, error) {
		return apps.NewApplySettingsAction(t.CacheFor(lease.Session())).Run(ctx, lease.Session(), in.App, in.Values)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_set_app_hidden",
		Description: "Queue visibility changes for a built-in or Connect IQ activity, glance or app. Applies on unplug. Includes a registry backup path when available.",
		Annotations: mcpx.Writes("Hide or show app", false, false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in hideIn) (hiddenResult, error) {
		backup, err := (apps.VisibilityAction{}).Run(ctx, lease.Session(), in.App, in.Hidden)
		if err != nil {
			return hiddenResult{}, err
		}

		return hiddenResult{Hidden: in.Hidden, Backup: backup, Note: "Applied when the USB session ends; ask the user to unplug the watch."}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_remove_app",
		Description: "Permanently delete an app, its settings and data. Unplug to finish removal. Built-in apps and the Connect IQ Store app are protected. Confirm with the user first.",
		Annotations: mcpx.Writes("Remove app", true, false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in rmIn) (removedResult, error) {
		removed, err := apps.NewRemoveAction(t.CacheFor(lease.Session())).Run(ctx, lease.Session(), in.App)
		if err != nil {
			return removedResult{}, err
		}

		return removedResult{Removed: removed, Note: "The watch finishes the removal when the USB session ends; ask the user to unplug it."}, nil
	}))
}
