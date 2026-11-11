package app

import (
	"context"
	"net"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/app/process"
	appshttp "github.com/DevWolk/garminctl/internal/containers/apps/ui/http"
	appsmcp "github.com/DevWolk/garminctl/internal/containers/apps/ui/mcp"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	connecthttp "github.com/DevWolk/garminctl/internal/containers/connect/ui/http"
	connectmcp "github.com/DevWolk/garminctl/internal/containers/connect/ui/mcp"
	healthhttp "github.com/DevWolk/garminctl/internal/containers/health/ui/http"
	healthmcp "github.com/DevWolk/garminctl/internal/containers/health/ui/mcp"
	musichttp "github.com/DevWolk/garminctl/internal/containers/music/ui/http"
	musicmcp "github.com/DevWolk/garminctl/internal/containers/music/ui/mcp"
	settingshttp "github.com/DevWolk/garminctl/internal/containers/settings/ui/http"
	settingsmcp "github.com/DevWolk/garminctl/internal/containers/settings/ui/mcp"
	storehttp "github.com/DevWolk/garminctl/internal/containers/store/ui/http"
	storemcp "github.com/DevWolk/garminctl/internal/containers/store/ui/mcp"
	updateshttp "github.com/DevWolk/garminctl/internal/containers/updates/ui/http"
	updatesmcp "github.com/DevWolk/garminctl/internal/containers/updates/ui/mcp"
	watchhttp "github.com/DevWolk/garminctl/internal/containers/watch/ui/http"
	watchmcp "github.com/DevWolk/garminctl/internal/containers/watch/ui/mcp"
	workoutshttp "github.com/DevWolk/garminctl/internal/containers/workouts/ui/http"
	workoutsmcp "github.com/DevWolk/garminctl/internal/containers/workouts/ui/mcp"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/mcpserver"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/web"
)

// Serve binds an HTTP listener before constructing its backend owners.
func Serve(ctx context.Context, addr string, open watchsession.Opener, autoSync bool, version string) error {
	var config net.ListenConfig

	listener, err := config.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return ServeListener(ctx, listener, open, autoSync, version)
}

// ServeListener assembles the shared HTTP/MCP/watch backend. The web shell owns
// the listener and drains/closes the supplied runtime when serving ends.
func ServeListener(ctx context.Context, listener net.Listener, open watchsession.Opener, autoSync bool, version string) error {
	runtime := NewRuntime(ctx, open)
	return serveListenerRuntime(listener, runtime, autoSync, version)
}

func serveListenerRuntime(listener net.Listener, runtime *process.Runtime, autoSync bool, version string) error {
	account := gc.New(gc.DefaultStorePath())

	return web.ServeRuntime(listener, runtime, account, autoSync, version, web.Features{
		HTTP: []httpx.RouteRegistrar{
			&connecthttp.Handler{Manager: runtime.Manager(), Client: account, Coordinator: runtime.Sync, CloudFor: ConnectFor(runtime, account), JobFor: func(epoch watchsession.ConnectionEpoch) *connect.Cloud {
				return JobConnect(runtime, account, epoch)
			}},
			&healthhttp.Handler{Manager: runtime.Manager(), Profile: fit.MustProfile(), Activities: runtime.Activities, AccessFor: HealthAccess(runtime, account)},
			&workoutshttp.Handler{Manager: runtime.Manager(), Client: account, DeliveryFor: WorkoutDelivery(runtime, account)},
			&musichttp.Handler{Manager: runtime.Manager()},
			&settingshttp.Handler{Manager: runtime.Manager(), Profile: fit.MustProfile()},
			&watchhttp.Handler{Manager: runtime.Manager(), Profile: fit.MustProfile()},
			&appshttp.Handler{Manager: runtime.Manager(), CacheFor: runtime.AppSchemaCacheFor},
			&storehttp.Handler{Manager: runtime.Manager(), Registry: StoreRegistry},
			&updateshttp.Handler{Manager: runtime.Manager(), Config: WatchUpdateConfig, Registry: UpdatesRegistry, InvalidateFor: updateInvalidator(runtime)},
		},
		MCP:             mcpTools(runtime, account),
		WatchSummary:    WatchSummary,
		WatchModel:      WatchModel,
		SoftwareUpdates: SoftwareUpdateNames,
		Refresh:         refreshWith(runtime.AppSchemaCacheFor),
		AutoSync:        autoSyncNotes(runtime.Sync, connectRunner(runtime, account)),
	})
}

// ServeMCP assembles a standalone stdio backend, with its own process owners.
func ServeMCP(ctx context.Context, open watchsession.Opener, version string) error {
	return ServeMCPTransport(ctx, open, version, &mcp.StdioTransport{})
}

// ServeMCPTransport assembles the same standalone backend over the caller's
// transport. It builds its own process owners and account client.
func ServeMCPTransport(ctx context.Context, open watchsession.Opener, version string, transport mcp.Transport) error {
	runtime := NewRuntime(ctx, open)
	account := gc.New(gc.DefaultStorePath())

	return mcpserver.ServeRuntime(runtime, transport, version, mcpTools(runtime, account)...)
}

func mcpTools(runtime *process.Runtime, account *gc.Client) []mcpx.ToolRegistrar {
	return []mcpx.ToolRegistrar{settingsTools(runtime), watchTools(runtime), appsTools(runtime), storeTools(runtime), updatesTools(runtime), musicTools(runtime), workoutsTools(runtime, account), healthTools(runtime, account), connectTools(runtime, account)}
}

func settingsTools(runtime *process.Runtime) *settingsmcp.Tools {
	return &settingsmcp.Tools{Manager: runtime.Manager(), Profile: fit.MustProfile(), BindContext: runtime.RequestContext}
}

func watchTools(runtime *process.Runtime) *watchmcp.Tools {
	return &watchmcp.Tools{Manager: runtime.Manager(), Profile: fit.MustProfile(), AppCount: WatchAppCount, BindContext: runtime.RequestContext}
}

func appsTools(runtime *process.Runtime) *appsmcp.Tools {
	return &appsmcp.Tools{Manager: runtime.Manager(), CacheFor: runtime.AppSchemaCacheFor, BindContext: runtime.RequestContext}
}

func storeTools(runtime *process.Runtime) *storemcp.Tools {
	return &storemcp.Tools{Manager: runtime.Manager(), Registry: StoreRegistry, BindContext: runtime.RequestContext}
}

func updatesTools(runtime *process.Runtime) *updatesmcp.Tools {
	return &updatesmcp.Tools{
		Manager:       runtime.Manager(),
		Config:        WatchUpdateConfig,
		Registry:      UpdatesRegistry,
		InvalidateFor: updateInvalidator(runtime),
		BindContext:   runtime.RequestContext,
	}
}

func musicTools(runtime *process.Runtime) *musicmcp.Tools {
	return &musicmcp.Tools{Manager: runtime.Manager(), BindContext: runtime.RequestContext}
}

func workoutsTools(runtime *process.Runtime, account *gc.Client) *workoutsmcp.Tools {
	return &workoutsmcp.Tools{Manager: runtime.Manager(), Client: account, DeliveryFor: WorkoutDelivery(runtime, account), BindContext: runtime.RequestContext}
}

func healthTools(runtime *process.Runtime, account *gc.Client) *healthmcp.Tools {
	return &healthmcp.Tools{Manager: runtime.Manager(), Profile: fit.MustProfile(), Activities: runtime.Activities, AccessFor: HealthAccess(runtime, account), BindContext: runtime.RequestContext}
}

func connectTools(runtime *process.Runtime, account *gc.Client) *connectmcp.Tools {
	return &connectmcp.Tools{Manager: runtime.Manager(), Coordinator: runtime.Sync, CloudFor: ConnectFor(runtime, account), JobFor: func(epoch watchsession.ConnectionEpoch) *connect.Cloud {
		return JobConnect(runtime, account, epoch)
	}, BindContext: runtime.RequestContext}
}
