# MCP server

Run `garminctl mcp` for the stdio transport. Claude Code can use this
configuration in `.mcp.json`:

```json
{ "mcpServers": { "garmin": { "command": "/home/ubuntu/projects/garmin/bin/garminctl", "args": ["mcp"] } } }
```

Replace the command path with the installed binary's path.

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

Sign in to Garmin Connect first through the CLI or web UI; MCP does not handle
the password. Every tool has a title, read-only/destructive and open-world
hints, and a typed output schema. List results are wrapped in objects such as
`entries`, `rows`, `apps`, and `days`.
