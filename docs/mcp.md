# MCP server

Connect an assistant through Streamable HTTP to share the desktop or web
server's watch connection and Garmin Connect session. Sign in through the app
or [CLI](cloud-sync.md) before using cloud tools; MCP does not handle passwords.

## HTTP connection

Launch the [desktop app](desktop.md) or run `garminctl serve`. Open **AI & MCP**,
choose the assistant and copy its command or configuration; the tab fills in the
server's own address. With the default address:

```sh
claude mcp add --transport http --scope user garminctl http://127.0.0.1:8787/mcp
codex mcp add garminctl --url http://127.0.0.1:8787/mcp
gemini mcp add --transport http --scope user garminctl http://127.0.0.1:8787/mcp
```

Antigravity reads `~/.gemini/config/mcp_config.json`; merge this entry into it:

```json
{"mcpServers":{"garminctl":{"serverUrl":"http://127.0.0.1:8787/mcp"}}}
```

In any other client, add the URL as a Streamable HTTP server.

The endpoint is available while that server runs. The desktop uses a loopback
address; a WSL client connecting to the Windows app needs access to Windows's
localhost, such as mirrored networking.

To check the connection, ask the assistant to call `watch_info`, then
`watch_list_files` with `path: "GARMIN/Activity"`. Check the returned device
and the calls' **Success** status in **AI & MCP**.

The tab shows client-reported names and versions, open sessions, last-request
times, and call status and duration. It retains up to 100 sessions and 100 calls
in memory, without arguments or response contents. Open sessions may be idle;
they end on explicit session termination or after 30 minutes without activity.

## Stdio connection

`garminctl mcp` runs a separate server over stdio. Configure the installed CLI
in the client's MCP settings, for example:

```json
{"mcpServers":{"garmin":{"command":"garminctl","args":["mcp"]}}}
```

Use an absolute executable path if the client does not inherit your `PATH`.
Global `-device ID` or `-root DIR` flags precede `mcp`. Stdio opens an independent
watch connection; its sessions do not appear in **AI & MCP**. Use HTTP to share
an application's existing USB connection.

## Tools

- Watch and files: `watch_info`, `watch_list_files`, `watch_decode_fit`,
  `watch_delete_file`.
- Watch settings: `watch_settings`, `watch_settings_schema`, `watch_set_settings`.
- Apps: `watch_apps`, `watch_app_settings`, `watch_app_settings_schema`,
  `watch_set_app_settings`, `watch_set_app_hidden`, `watch_remove_app`.
- Updates: `watch_check_updates`, `watch_install_update`.
- Garmin Connect: `cloud_status`, `cloud_sync`, `cloud_settings`, `cloud_set_settings`.
- Store: `store_search`, `store_app`.
- Health and activities: `activities_list`, `activity_detail`, `health_daily`,
  `health_history`, `health_extras`, `health_log_weight`, `health_day`.
- Workouts: `workouts_list`, `workout_create`, `workout_send`.
- Music: `music_list`, `music_playlist`.

Tools advertise input/output schemas and read-only, destructive, and open-world
hints. List outputs are objects with keys such as `entries`, `rows`, or `apps`.

Sources: [tool schemas](../internal/containers) (each feature's `ui/mcp`),
[HTTP sessions](../internal/ship/mcpx/http.go).
