# MCP server

MCP (Model Context Protocol) lets an AI assistant use garminctl: read the
watch, change settings, sync with Garmin Connect. garminctl offers it in two
ways.

| Connection | Use it when | Watch connection |
| --- | --- | --- |
| [HTTP](#http-connection) | The desktop app or `garminctl serve` is running. | Shared with the app, including its Garmin Connect sign-in. |
| [Stdio](#stdio-connection) | The assistant should start garminctl itself. | Its own, separate from any running app. |

Sign in to Garmin Connect through the app or the [CLI](cloud-sync.md) before
using the tools that need it. MCP has no sign-in tool, and the assistant is told
never to ask for your password.

## HTTP connection

1. Open the [desktop app](desktop.md) or run `garminctl serve`.
2. Open **AI & MCP** and choose your assistant: Claude Code, Codex CLI, Gemini
   CLI or Antigravity.
3. Copy the command or configuration that the tab shows. It already contains
   the server's address.
4. For a command, paste it into a terminal and run it once. For a
   configuration, merge it into the file that the tab names.

With the default address, the commands are:

```sh
# Claude Code
claude mcp add --transport http --scope user garminctl http://127.0.0.1:8787/mcp
# Codex CLI
codex mcp add garminctl --url http://127.0.0.1:8787/mcp
# Gemini CLI
gemini mcp add --transport http --scope user garminctl http://127.0.0.1:8787/mcp
```

Antigravity reads `~/.gemini/config/mcp_config.json`; merge this entry into it:

```json
{"mcpServers":{"garminctl":{"serverUrl":"http://127.0.0.1:8787/mcp"}}}
```

In another assistant, add `http://127.0.0.1:8787/mcp` as an MCP server of the
type "Streamable HTTP".

The endpoint is available while the server runs. A client inside WSL that
connects to the app on Windows needs access to Windows's localhost, for example
through mirrored networking.

### Check the connection

1. Ask the assistant to call `watch_info`.
2. Ask it to call `watch_list_files` with `path: "GARMIN/Activity"`.
3. In **AI & MCP**, check that the watch is the right one and that both calls
   show **Success**.

### What the AI & MCP tab records

The tab shows the names and versions that clients report, open sessions, the
time of the last request, and each call's status and duration. It keeps up to
100 sessions and 100 calls in memory, without arguments or results. An open
session can be idle; it ends when the client closes it or after 30 minutes
without activity.

## Stdio connection

`garminctl mcp` runs a separate server on standard input and output. Add the
installed CLI to the client's MCP settings, for example:

```json
{"mcpServers":{"garmin":{"command":"garminctl","args":["mcp"]}}}
```

- Use the absolute path of the executable if the client does not inherit your
  `PATH`.
- Global flags go before `mcp`: `-device ID` or `-root DIR`.
- A stdio server opens its own watch connection, and its sessions do not
  appear in **AI & MCP**. Use HTTP to share the connection of a running app.

## Tools

"Changes" marks tools that write to the watch or to Garmin Connect. "Sign-in"
marks tools that need Garmin Connect. Every tool except `cloud_status` needs
the watch to be connected, including the store and workout tools.

| Tool | What it does | Changes | Sign-in |
| --- | --- | --- | --- |
| `watch_info` | Model, unit ID, software, app count, pending files and storage. | | |
| `watch_list_files` | List a directory on the watch. | | |
| `watch_decode_fit` | Decode a FIT file; returns up to 400 messages. | | |
| `watch_delete_file` | Delete one file. There is no undo. | Yes | |
| `watch_settings` | Read watch settings with values, options and notes. | | |
| `watch_settings_schema` | List the documented settings fields, units and options. | | |
| `watch_set_settings` | Queue settings changes; they apply after you unplug the watch. | Yes | |
| `watch_apps` | List installed apps, watch faces, glances and activities. | | |
| `watch_app_settings` | Read a Connect IQ app's settings. | | |
| `watch_app_settings_schema` | Labels, options and ranges of an app's settings, from the store. | | |
| `watch_set_app_settings` | Save app settings to the watch at once. | Yes | |
| `watch_set_app_hidden` | Queue hiding or showing an app; applies after you unplug. | Yes | |
| `watch_remove_app` | Delete an app with its settings and data. | Yes | |
| `watch_check_updates` | Check for watch software and app updates. Installs nothing. | | |
| `watch_install_update` | Download, verify and write one software update; the watch installs it after you unplug. | Yes | |
| `cloud_status` | Sign-in and account status. Works without a watch. | | |
| `cloud_sync` | Upload recordings, then download queued files to the watch. | Yes | Yes |
| `cloud_settings` | Read the watch settings saved in Connect. | | Yes |
| `cloud_set_settings` | Change Connect settings and fetch the file Connect generates. | Yes | Yes |
| `store_search` | Search the Connect IQ store for apps that fit the watch. | | |
| `store_app` | App details, permissions and reviews. | | |
| `activities_list` | List recorded activities from the watch. | | |
| `activity_detail` | One activity with laps and a GPS track. | | |
| `health_daily` | Daily health: steps, heart rate, stress, sleep, calories. | | Yes |
| `health_history` | Up to a year of daily health. | | Yes |
| `health_extras` | Fitness metrics, records, badges and weight history. | | Yes |
| `health_day` | One day's heart rate, stress, body battery and sleep stages. | | Yes |
| `health_log_weight` | Add a weigh-in to Connect. | Yes | Yes |
| `workouts_list` | List Connect workouts. | | Yes |
| `workout_create` | Create a workout in Connect and send it to the watch, or write it to the watch only. | Yes | Unless local |
| `workout_send` | Send a Connect workout to the watch. | Yes | Yes |
| `music_list` | List audio files, playlists and streaming-app cache sizes. | | |
| `music_playlist` | Write a playlist from tracks already on the watch. | Yes | |

`watch_delete_file` and `watch_remove_app` are marked destructive. Primary
firmware installed with `watch_install_update` restarts the watch. The server's
instructions tell the assistant to ask you before removing an app or installing
firmware.

For client developers: every tool describes its input and output and carries
read-only, destructive and open-world hints. List results are objects with keys
such as `entries`, `rows` or `apps`.

MCP has no tool for installing apps from the store, uploading music, or
updating garminctl itself. Apps arrive through `cloud_sync` or a `.PRG` file
in the [web UI](web-ui.md).

Sources: [tool schemas](../internal/containers) (each feature's `ui/mcp`),
[server instructions](../internal/ship/mcpserver/server.go),
[HTTP sessions](../internal/ship/mcpx/http.go).
