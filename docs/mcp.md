# MCP server

## Connect to the application

Launch the [installed desktop application](desktop.md), then open **AI & MCP**.
Copy the displayed URL into any client supporting MCP Streamable HTTP. With
Claude Code, the default command is:

```sh
claude mcp add --transport http --scope user garminctl http://127.0.0.1:8787/mcp
```

HTTP shares the application's watch and Garmin Connect session. It is local-only
and available while the server is running. On WSL with the Windows application,
the WSL client must be able to reach Windows's localhost (for example with WSL
mirrored networking); the server does not bind to the LAN.

Other MCP clients use the same URL and the **Streamable HTTP** transport;
register it through that client's HTTP-server configuration. Keep the desktop
application running while using its tools. No separate MCP executable or
`garminctl mcp` process is needed for this connection. For the browser-only
workflow, `garminctl serve` exposes the same endpoint.

The tab lists client-reported name/version, connection and last-request times,
open sessions, and recent tool successes/errors. An open session can be idle;
Last seen records actual requests. History is limited to 100 sessions and calls,
kept only in memory, and excludes arguments and response contents.

## Verify the installed app

1. Stop development `garminctl serve` processes and disable the old background
   server with `garminctl autostart off`, if enabled. Launch garminctl from the
   OS shortcut installed by Setup/DEB/RPM.
2. Confirm the process listening on the displayed MCP port is the installed
   `garmin-desktop` executable. A compatible server started earlier can be
   reused by the app, so the URL alone does not establish which build owns it.
3. Connect the assistant to the URL copied from **AI & MCP** and ask it to call
   `watch_info`, then `watch_list_files` with `path: "GARMIN/Activity"`.
4. Check that the returned model is your watch and that the application's tab
   shows the assistant session and both calls with **Success**. Close the MCP
   session through a client that sends session termination (`DELETE /mcp`)
   and check that it moves to Recent clients. Clients that do not terminate
   their session can remain listed until the 30-minute idle timeout.
5. Close the app. When it started its own server, the MCP endpoint must stop
   answering too; reopening the app creates a fresh session/call history.

On Windows, these PowerShell commands identify the owner of the default port:

```powershell
$listener = Get-NetTCPConnection -State Listen -LocalPort 8787 | Select-Object -First 1
Get-Process -Id $listener.OwningProcess | Select-Object Id, Path
```

For the default installation, expect
`%LOCALAPPDATA%\Programs\garminctl\garmin-desktop.exe`. On native Linux, use
`ss -ltnp 'sport = :8787'` and expect the package's `/usr/bin/garmin-desktop`.
Use the displayed port if you chose another address.

Running `bin/garminctl info` or a temporary server from a source checkout
checks that build's CLI/transport, and does not complete the installed-app
MCP check. Recorded results and outstanding blockers are in
[validation status](desktop-validation.md).

## Standalone stdio

Run `garminctl mcp` for the stdio transport. This starts a separate server; its
sessions do not appear in the application's tab, and it should not compete with
the app for the same USB watch. Prefer HTTP when the app is running. Claude Code can use this
configuration in `.mcp.json`:

```json
{ "mcpServers": { "garmin": { "command": "/usr/bin/garminctl", "args": ["mcp"] } } }
```

Replace the command path with the installed binary's path.

## Available tools

The 33 tools are:

- Watch: `watch_info`, `watch_list_files`, `watch_decode_fit`,
  `watch_delete_file`, `watch_settings`, `watch_settings_schema`,
  `watch_set_settings`, `watch_apps`, `watch_app_settings`,
  `watch_app_settings_schema`, `watch_set_app_settings`,
  `watch_check_updates`, `watch_set_app_hidden`, `watch_remove_app`,
  `watch_install_update`.
- Garmin Connect: `cloud_status`, `cloud_sync`, `cloud_settings`,
  `cloud_set_settings`.
- Store: `store_search`, `store_app`.
- Health and activities: `activities_list`, `activity_detail`, `health_daily`,
  `health_history`, `health_extras`, `health_log_weight`, `health_day`.
- Workouts and music: `workouts_list`, `workout_create`, `workout_send`,
  `music_list`, `music_playlist`.

Sign in to Garmin Connect first through the app, CLI, or web UI; MCP does not handle
the password. Every tool has a title, read-only/destructive and open-world
hints, and a typed output schema. List results are wrapped in objects such as
`entries`, `rows`, `apps`, and `days`.
