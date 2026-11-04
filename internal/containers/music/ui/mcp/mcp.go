// Package mcp adapts local music Actions to typed MCP tools.
package mcp

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/music"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tools uses the runtime's shared Manager and lifecycle context binding.
type (
	Tools struct {
		Manager     *watchsession.Manager
		BindContext func(context.Context) (context.Context, context.CancelFunc)
	}
	empty struct{}
	plIn  struct {
		Name   string   `json:"name" jsonschema:"playlist name"`
		Tracks []string `json:"tracks" jsonschema:"watch paths from music_list, in order"`
	}
)

// RegisterTools preserves the two music tools, their typed outputs and annotations.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "music_list",
		Description: "List audio files, playlists and streaming app cache sizes. Streaming downloads are excluded. Use track paths with music_playlist.",
		Annotations: mcpx.ReadOnly("List music", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, _ empty) (*music.Library, error) {
		return (music.ListAction{}).Run(ctx, lease.Session())
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "music_playlist",
		Description: "Write a playlist (Music/<name>.m3u) from tracks already on the watch; an existing playlist of that name is replaced.",
		Annotations: mcpx.Writes("Write playlist", false, false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in plIn) (*music.Playlist, error) {
		return (music.WritePlaylistAction{}).Run(ctx, lease.Session(), in.Name, in.Tracks)
	}))
}
