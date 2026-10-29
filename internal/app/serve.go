package app

import (
	"context"
	"net"

	settingshttp "github.com/DevWolk/garminctl/internal/containers/settings/ui/http"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/mcpserver"
	"github.com/DevWolk/garminctl/internal/web"
)

// Serve binds an HTTP listener before constructing its backend owners.
func Serve(ctx context.Context, addr string, open garmin.Opener, autoSync bool, version string) error {
	var config net.ListenConfig

	listener, err := config.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return ServeListener(ctx, listener, open, autoSync, version)
}

// ServeListener assembles the shared HTTP/MCP/watch backend. The web shell owns
// the listener and drains/closes the supplied runtime when serving ends.
func ServeListener(ctx context.Context, listener net.Listener, open garmin.Opener, autoSync bool, version string) error {
	runtime := NewRuntime(ctx, open)
	account := gc.New(gc.DefaultStorePath())

	return web.ServeRuntime(listener, runtime, account, autoSync, version,
		&settingshttp.Handler{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile()})
}

// ServeMCP assembles a standalone stdio backend, with its own process owners.
func ServeMCP(ctx context.Context, open garmin.Opener, version string) error {
	runtime := NewRuntime(ctx, open)
	account := gc.New(gc.DefaultStorePath())

	return mcpserver.ServeRuntime(runtime, account, version)
}
