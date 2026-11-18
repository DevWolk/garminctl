// Package mcp adapts settings Actions to typed MCP tools.
package mcp

import (
	"context"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/containers/settings"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Tools registers settings tools using the backend's existing watch owner.
// BindContext supplies the backend lifetime as well as caller cancellation.
type Tools struct {
	Manager     *watchsession.Manager
	Profile     *fit.Profile
	BindContext func(context.Context) (context.Context, context.CancelFunc)
}

type settingsIn struct {
	IncludeUnknown bool   `json:"includeUnknown,omitempty" jsonschema:"also return undocumented fields (numbers only)"`
	Message        string `json:"message,omitempty" jsonschema:"restrict to one message, e.g. deviceSettings or userProfile"`
}

type schemaIn struct {
	Message string `json:"message" jsonschema:"message name or number, e.g. deviceSettings, userProfile, hrZone, zonesTarget"`
}

type setIn struct {
	Changes []settings.Change `json:"changes" jsonschema:"field changes: mesg and field (name or number), value (enum name, number or scaled value), optional index for repeated messages and arrayIndex for array fields"`
}

type settingsResult struct {
	Rows []settings.Row `json:"rows"`
}

type fieldsResult struct {
	Fields []settings.SchemaField `json:"fields"`
}

// RegisterTools preserves the three settings tools and their schema metadata.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_settings",
		Description: "Read watch settings with values, types, options and notes. Use watch_app_settings for Connect IQ apps and cloud_settings for Garmin Connect's copy.",
		Annotations: mcpx.ReadOnly("Read watch settings", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in settingsIn) (settingsResult, error) {
		view, err := settings.NewReadAction(t.Profile).Run(ctx, lease.Session())
		if err != nil {
			return settingsResult{}, err
		}

		return settingsResult{Rows: filterRows(view.Rows(), in)}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_settings_schema",
		Description: "List documented settings fields, types, units and options. Use before watch_set_settings.",
		Annotations: mcpx.ReadOnly("Settings field schema", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(_ context.Context, _ *watchsession.Lease, in schemaIn) (fieldsResult, error) {
		fields, err := settings.NewSchemaAction(t.Profile).Run(in.Message)
		return fieldsResult{Fields: fields}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_set_settings",
		Description: "Queue watch settings changes and return their previous values. Tell the user to unplug the watch to apply them.",
		Annotations: mcpx.Writes("Change watch settings", false, false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in setIn) (*settings.ApplyResult, error) {
		return settings.NewApplyAction(t.Profile).Run(ctx, lease.Session(), in.Changes)
	}))
}

func filterRows(rows []settings.Row, in settingsIn) []settings.Row {
	out := make([]settings.Row, 0, len(rows))

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
