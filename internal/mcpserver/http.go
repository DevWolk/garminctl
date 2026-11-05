package mcpserver

import (
	"context"

	"github.com/DevWolk/garminctl/internal/ship/mcpx"
)

// Client preserves the public MCP session metadata name during extraction.
type Client = mcpx.Client

// Activity preserves the public bounded tool-call metadata name.
type Activity = mcpx.Activity

// Snapshot preserves the HTTP MCP metadata response shape.
type Snapshot = mcpx.Snapshot

// HTTPServer preserves the shell name for the shared protocol tracker.
type HTTPServer = mcpx.HTTPServer

// NewHTTPServer supplies the registered Garmin tools and process lifetime to mcpx.
func NewHTTPServer(parent context.Context, version string, registrars ...mcpx.ToolRegistrar) *HTTPServer {
	protocol := newServer(version, registrars...)
	return mcpx.NewHTTPServer(parent, protocol)
}
