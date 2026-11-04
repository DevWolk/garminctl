package app

import (
	"context"
	"net"

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
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/mcpserver"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
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

	return web.ServeRuntime(listener, runtime, account, autoSync, version, web.Features{
		HTTP: []httpx.RouteRegistrar{
			&connecthttp.Handler{Manager: runtime.Watch.Manager(), Client: account, Coordinator: runtime.Sync, CloudFor: ConnectFor(runtime.Watch, account), JobFor: func(epoch watchsession.ConnectionEpoch) *connect.Cloud {
				return JobConnect(runtime.Watch, account, epoch)
			}},
			&healthhttp.Handler{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile(), Activities: runtime.Activities, AccessFor: HealthAccess(runtime.Watch, account)},
			&workoutshttp.Handler{Manager: runtime.Watch.Manager(), Client: account, DeliveryFor: WorkoutDelivery(runtime.Watch, account)},
			&musichttp.Handler{Manager: runtime.Watch.Manager()},
			&settingshttp.Handler{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile()},
			&watchhttp.Handler{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile()},
			&appshttp.Handler{Manager: runtime.Watch.Manager(), CacheFor: runtime.Watch.AppSchemaCacheFor},
			&storehttp.Handler{Manager: runtime.Watch.Manager(), Registry: StoreRegistry},
			&updateshttp.Handler{Manager: runtime.Watch.Manager(), Config: WatchUpdateConfig, Registry: UpdatesRegistry, InvalidateFor: updateInvalidator(runtime)},
		},
		MCP:          []mcpx.ToolRegistrar{settingsTools(runtime), watchTools(runtime), appsTools(runtime), storeTools(runtime), updatesTools(runtime), musicTools(runtime), workoutsTools(runtime, account), healthTools(runtime, account), connectTools(runtime, account)},
		WatchSummary: WatchSummary,
		SyncRunner:   connectRunner(runtime.Watch, account),
	})
}

// ServeMCP assembles a standalone stdio backend, with its own process owners.
func ServeMCP(ctx context.Context, open garmin.Opener, version string) error {
	runtime := NewRuntime(ctx, open)
	account := gc.New(gc.DefaultStorePath())

	return mcpserver.ServeRuntime(runtime, account, version, settingsTools(runtime), watchTools(runtime), appsTools(runtime), storeTools(runtime), updatesTools(runtime), musicTools(runtime), workoutsTools(runtime, account), healthTools(runtime, account), connectTools(runtime, account))
}

func settingsTools(runtime *garmin.Runtime) *settingsmcp.Tools {
	return &settingsmcp.Tools{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile(), BindContext: runtime.RequestContext}
}

func watchTools(runtime *garmin.Runtime) *watchmcp.Tools {
	return &watchmcp.Tools{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile(), AppCount: WatchAppCount, BindContext: runtime.RequestContext}
}

func appsTools(runtime *garmin.Runtime) *appsmcp.Tools {
	return &appsmcp.Tools{Manager: runtime.Watch.Manager(), CacheFor: runtime.Watch.AppSchemaCacheFor, BindContext: runtime.RequestContext}
}

func storeTools(runtime *garmin.Runtime) *storemcp.Tools {
	return &storemcp.Tools{Manager: runtime.Watch.Manager(), Registry: StoreRegistry, BindContext: runtime.RequestContext}
}

func updatesTools(runtime *garmin.Runtime) *updatesmcp.Tools {
	return &updatesmcp.Tools{
		Manager:       runtime.Watch.Manager(),
		Config:        WatchUpdateConfig,
		Registry:      UpdatesRegistry,
		InvalidateFor: updateInvalidator(runtime),
		BindContext:   runtime.RequestContext,
	}
}

func musicTools(runtime *garmin.Runtime) *musicmcp.Tools {
	return &musicmcp.Tools{Manager: runtime.Watch.Manager(), BindContext: runtime.RequestContext}
}

func workoutsTools(runtime *garmin.Runtime, account *gc.Client) *workoutsmcp.Tools {
	return &workoutsmcp.Tools{Manager: runtime.Watch.Manager(), Client: account, DeliveryFor: WorkoutDelivery(runtime.Watch, account), BindContext: runtime.RequestContext}
}

func healthTools(runtime *garmin.Runtime, account *gc.Client) *healthmcp.Tools {
	return &healthmcp.Tools{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile(), Activities: runtime.Activities, AccessFor: HealthAccess(runtime.Watch, account), BindContext: runtime.RequestContext}
}

func connectTools(runtime *garmin.Runtime, account *gc.Client) *connectmcp.Tools {
	return &connectmcp.Tools{Manager: runtime.Watch.Manager(), Coordinator: runtime.Sync, CloudFor: ConnectFor(runtime.Watch, account), JobFor: func(epoch watchsession.ConnectionEpoch) *connect.Cloud {
		return JobConnect(runtime.Watch, account, epoch)
	}, BindContext: runtime.RequestContext}
}
