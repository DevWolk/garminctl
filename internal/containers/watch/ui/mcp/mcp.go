// Package mcp adapts watch Actions and file operations to typed MCP tools.
package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const maxDecodedMsgs = 400

// Tools registers watch tools using the backend's existing watch owner.
// BindContext supplies the backend lifetime as well as caller cancellation.
type Tools struct {
	Manager     *watchsession.Manager
	Profile     *fit.Profile
	AppCount    watch.AppCount
	BindContext func(context.Context) (context.Context, context.CancelFunc)
}

type empty struct{}

type listIn struct {
	Path string `json:"path" jsonschema:"directory on the watch, e.g. GARMIN/Activity; empty for the storage root"`
}

type pathIn struct {
	Path string `json:"path" jsonschema:"file path on the watch, e.g. GARMIN/Sports/WalkingWalk.fit"`
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

// RegisterTools preserves the four watch tools and their schema metadata.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_info",
		Description: "Read the connected watch's model, unit ID, software, app count, pending files and storage.",
		Annotations: mcpx.ReadOnly("Watch summary", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, _ empty) (*watch.Summary, error) {
		return watch.NewSummaryAction(t.Profile, t.AppCount).Run(ctx, lease.Session())
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_list_files",
		Description: "List watch files with names, sizes and modification times. Use their paths with watch_decode_fit or watch_delete_file.",
		Annotations: mcpx.ReadOnly("List watch files", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in listIn) (filesResult, error) {
		entries, err := lease.Session().List(ctx, in.Path)
		return filesResult{Entries: entries}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "watch_decode_fit",
		Description: "Decode a watch FIT file into named messages and fields. Undocumented fields use numbers. Returns up to 400 messages.",
		Annotations: mcpx.ReadOnly("Decode FIT file", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in pathIn) (messagesResult, error) {
		msgs, err := watch.NewDecodeAction(t.Profile).Run(ctx, lease.Session(), in.Path)
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
		Annotations: mcpx.Writes("Delete watch file", true, false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in pathIn) (deletedResult, error) {
		if err := lease.Session().DeleteFile(ctx, in.Path); err != nil {
			return deletedResult{}, err
		}

		return deletedResult{Deleted: in.Path}, nil
	}))
}
