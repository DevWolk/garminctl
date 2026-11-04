// Package mcp adapts catalog Actions to typed MCP tools.
package mcp

import (
	"context"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/containers/store"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const storePageSize = 20

// Tools shares the runtime's existing watch owner and request context binding.
type Tools struct {
	Manager     *watchsession.Manager
	Registry    func(*watchsession.Session) store.InstalledApps
	BindContext func(context.Context) (context.Context, context.CancelFunc)
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

// RegisterTools preserves catalog tool schemas, annotations and watch selection.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "store_search",
		Description: "Find compatible Connect IQ apps with install and update status. No sign-in needed. Use a result's id with store_app.",
		Annotations: mcpx.ReadOnly("Search Connect IQ store", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in storeSearchIn) (*store.View, error) {
		dx, err := lease.Session().DeviceXML(ctx)
		if err != nil {
			return nil, err
		}

		q := ciqstore.StoreQuery{Keywords: in.Query, Type: in.Type, Sort: in.Sort, Page: in.Page, PageSize: storePageSize}

		return store.NewBrowseAction(t.Registry(lease.Session())).Run(ctx, dx.PartNumber, q)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "store_app",
		Description: "Read app details, version, size, permissions, price, reviews and install status. Use the Connect IQ phone app to install; cloud_sync downloads queued files.",
		Annotations: mcpx.ReadOnly("Store app details", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in storeAppIn) (*store.Detail, error) {
		dx, err := lease.Session().DeviceXML(ctx)
		if err != nil {
			return nil, err
		}

		return store.NewDetailAction(t.Registry(lease.Session())).Run(ctx, dx.PartNumber, in.ID)
	}))
}
