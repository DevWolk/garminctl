// Package mcpserver exposes watch and Garmin Connect tools over MCP.
package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/ship/mcpx"
)

// Runtime supplies the lifetime and joined shutdown of an app-owned backend.
type Runtime interface {
	Context() context.Context
	Close(context.Context) error
}

const instructions = `Manage a Garmin watch over USB and access Garmin Connect after sign-in.

Use watch_settings_schema before changing watch settings. Address fields by message and field name
(deviceSettings.backlightMode) or number. watch_set_settings queues changes for the next USB disconnect;
tell the user to unplug the watch. App settings use watch_app_settings and watch_set_app_settings,
which save directly to the watch.
Use cloud_set_settings for fields marked managedByConnect so future Connect syncs use the same values.
Sign in through the web UI or "garminctl cloud login"; never ask for a password in this conversation.
cloud_sync uploads recorded files and downloads queued settings, apps, workouts and courses.
watch_install_update verifies downloads and writes them to the watch for installation on unplug.
Primary firmware updates restart the watch; ask the user before installing them. Confirm app removal first.
App installations and updates can arrive through the Connect queue or a .PRG file uploaded in the web UI.

Use IDs returned by the tools: watch_apps for app tools; watch_check_updates software partNumber for
watch_install_update; activities_list.file for activity_detail; workouts_list.id for workout_send;
music_list track paths for music_playlist; store_search.id for store_app.`

// ServeRuntime runs the supplied MCP transport with process owners from app wiring.
// It closes the supplied runtime before returning.
func ServeRuntime(runtime Runtime, transport mcp.Transport, version string, registrars ...mcpx.ToolRegistrar) (err error) {
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(runtime.Context()), 3*time.Second)
		defer cancel()

		err = errors.Join(err, runtime.Close(closeCtx))
	}()

	srv := newServer(version, registrars...)

	return srv.Run(runtime.Context(), transport)
}

func newServer(version string, registrars ...mcpx.ToolRegistrar) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "garminctl", Version: version}, &mcp.ServerOptions{Instructions: instructions})

	for _, registrar := range registrars {
		registrar.RegisterTools(srv)
	}

	return srv
}
