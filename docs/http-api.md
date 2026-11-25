# HTTP API

`garminctl serve` and the desktop app serve a JSON API under `/api/` on the same
address as the [web UI](web-ui.md), `http://127.0.0.1:8787` by default. The
embedded web UI is its client. The paths carry no version number.

For an assistant, use [MCP](mcp.md). For scripts, the [CLI](cli.md) prints JSON
with `-json`.

```sh
curl http://127.0.0.1:8787/api/server
curl 'http://127.0.0.1:8787/api/status?connect=1'
curl 'http://127.0.0.1:8787/api/files?path=GARMIN/Activity'
```

## Access rules

The API has no login. The server accepts a request only when all of these hold:

- The `Host` header is `localhost` or an IP address, with the server's port.
- An `Origin` header, when present, is the server's own origin.
- The browser did not mark the request as started by another site
  (`Sec-Fetch-Site`). A plain link from another site into a UI page is allowed;
  a link or call to `/api/` or `/mcp` is not.

Any other request gets `403`. Programs on the same computer, such as `curl`,
send none of these browser headers and are accepted.

> [!WARNING]
> Keep the server on a loopback address. On any other address, everyone who can
> reach the port can change the watch and use the signed-in Garmin Connect
> account. See [the local server](architecture.md#the-local-server).

## Requests and responses

- Responses are JSON, except `GET /api/file`, which downloads the file.
- An error is `{"error": "message"}`. When the watch cannot be opened, most
  routes answer `{"connected": false, "error": "message"}`.
- A JSON request body holds one JSON value of at most 1 MiB. File uploads take
  the raw file as the body: at most 64 MiB, or 512 MiB for audio. A larger body
  gets `413`.
- Most routes open the watch when it is not open yet, and answer `503` when
  they cannot. Routes marked "No watch needed" answer without it.

| Status | Meaning |
| --- | --- |
| `200` | Done. |
| `202` | A sync started; poll `GET /api/cloud/sync`. |
| `400` | A missing or invalid parameter or body. |
| `401` | Not signed in to Garmin Connect, or the sign-in was refused. |
| `403` | The request did not pass the [access rules](#access-rules). |
| `404` | The watch path does not exist. |
| `408` | The request was cancelled. |
| `409` | The watch or the application is busy, a sync is already running, or an application update is in progress. |
| `413` | The body is too large. |
| `503` | The watch is not connected, or the server is stopping. |
| `504` | The operation timed out. |

Changes follow the same rules as in the other interfaces: watch settings and
app-list changes take effect after you unplug the watch. See
[when changes take effect](cli.md#when-changes-take-effect).

## Server and connection

| Route | Purpose |
| --- | --- |
| `GET /api/server` | Server identity: `application`, `version`, `source` (the watch or directory it reads) and `desktopAPI`, which the desktop app checks before it reuses a server. No watch needed. |
| `GET /api/status` | Connection state, the watch summary and automatic-sync events. Does not open the watch unless it is already open or `?connect=1` is set. |
| `POST /api/connect` | Open the watch and return its summary. |
| `POST /api/disconnect` | Close the connection. No watch needed. |
| `POST /api/refresh` | Drop cached watch data so the next request rereads it. |
| `GET /api/mcp` | The MCP endpoint address, with open sessions and recent tool calls. No watch needed. |

## Files

| Route | Purpose |
| --- | --- |
| `GET /api/files?path=P` | List a directory. |
| `GET /api/file?path=P` | Download a file. |
| `POST /api/file?path=P` | Write the request body to a file. |
| `DELETE /api/file?path=P` | Delete a file. |
| `GET /api/decode?path=P` | Decode a FIT file. |

## Watch settings

| Route | Purpose |
| --- | --- |
| `GET /api/settings` | Read settings as `rows`. |
| `GET /api/schema?mesg=M` | Describe the fields of one settings message, by name or number. |
| `POST /api/settings/apply` | Queue changes. Body: `{"changes": [{"mesg": "deviceSettings", "field": "backlightMode", "value": "manual"}]}`. Optional `index` selects a repeated message and `arrayIndex` an array element. |

## Apps

`{ref}` is an app name or file ID from `GET /api/apps`. The `hidden` route also
accepts `KIND:ID` for one entry of an app, as in the
[CLI](cli.md#settings-and-apps).

| Route | Purpose |
| --- | --- |
| `GET /api/apps` | List installed apps, watch faces, glances and activities. |
| `GET /api/apps/{ref}/settings` | Read a Connect IQ app's settings. |
| `PUT /api/apps/{ref}/settings` | Change app settings. Body: `{"values": {"KEY": VALUE}}`. |
| `GET /api/apps/{ref}/schema` | Labels, options and ranges of an app's settings. |
| `PUT /api/apps/{ref}/hidden` | Queue a visibility change. Body: `{"hidden": true}`. |
| `GET /api/apps/pending` | Show queued app-list changes. |
| `DELETE /api/apps/pending` | Cancel queued app-list changes. |
| `POST /api/apps/install` | Sideload an app. The body is the `.PRG` file. |
| `DELETE /api/apps/{ref}` | Remove a Connect IQ app. |

## Health and activities

Activities come from the watch. The other routes read Garmin Connect and need
sign-in.

| Route | Purpose |
| --- | --- |
| `GET /api/health/activities` | List decoded activities. `?wait=1` waits until every file is decoded. |
| `GET /api/health/activities/{name}` | One activity with laps and a GPS track. |
| `GET /api/health/daily?days=N` | Daily health. More than 14 days uses the history data, up to 366 days. |
| `GET /api/health/day?date=YYYY-MM-DD` | Intraday heart rate, stress, body battery and sleep. |
| `GET /api/health/extras` | Fitness metrics, records, badges and weight history. |
| `POST /api/health/weight` | Log a weigh-in. Body: `{"kg": 72.5}`. |

## Workouts

`spec` is a [workout specification](cli.md#workout-specification). Every route
here except `preview` needs a connected watch, including the two that only talk
to Garmin Connect.

| Route | Purpose |
| --- | --- |
| `GET /api/workouts` | List Garmin Connect workouts. |
| `POST /api/workouts/preview` | Validate a specification and show what Connect would receive. Body: `{"spec": {...}}`. No watch needed. |
| `POST /api/workouts` | Create a workout. Body: `{"spec": {...}, "send": false, "local": false}`. `send` also delivers it to the watch; `local` writes it to the watch without Connect. |
| `POST /api/workouts/{id}/send` | Deliver a Connect workout to the watch. |
| `DELETE /api/workouts/{id}` | Delete a workout in Connect. |

## Music

| Route | Purpose |
| --- | --- |
| `GET /api/music` | List tracks, playlists and streaming-app cache sizes. |
| `POST /api/music?folder=F&name=N` | Upload one audio file; the body is the file. |
| `DELETE /api/music?path=P` | Delete a track. |
| `PUT /api/music/playlists/{name}` | Write a playlist. Body: `{"tracks": ["PATH"]}`. |

## Store and updates

The two store routes need no sign-in and no watch. With a watch connected,
results are filtered for it and show what is installed.

| Route | Purpose |
| --- | --- |
| `GET /api/store?q=WORDS&type=T&sort=S&page=N` | Search or browse the Connect IQ store. |
| `GET /api/store/apps/{id}` | App details and reviews. |
| `GET /api/updates/software` | Check Garmin for watch software and data updates. |
| `POST /api/updates/software/install` | Download, verify and write one update. Body: `{"partNumber": "..."}`. |
| `GET /api/updates/apps` | Compare installed app versions with the store. |
| `GET /api/software` | List installed software components. |

## Garmin Connect

| Route | Purpose |
| --- | --- |
| `GET /api/cloud/status` | Sign-in and account status. No watch needed. |
| `POST /api/cloud/login` | Sign in. Body: `{"email": "...", "password": "..."}`. The password is not stored. No watch needed. |
| `POST /api/cloud/mfa` | Finish a sign-in that asked for a code. Body: `{"code": "..."}`. No watch needed. |
| `POST /api/cloud/logout` | Sign out and delete the saved session. No watch needed. |
| `POST /api/cloud/sync` | Start a sync and return `202`. An empty body syncs both directions; `{"upload": false}` or `{"download": false}` turns one off. |
| `GET /api/cloud/sync` | The running or last sync: `running`, `log`, `result`, `error`. No watch needed. |
| `GET /api/cloud/messages` | Files Garmin Connect has queued for the watch. |
| `GET /api/cloud/settings` | Watch settings saved in Connect. |
| `PUT /api/cloud/settings` | Change Connect settings. Body: `{"changes": {"KEY": VALUE}}`. |

## MCP and pages

`GET`, `POST` and `DELETE /mcp` are the [MCP](mcp.md) endpoint. `/`, and each
tab path such as `/files/GARMIN/Apps`, return the web UI.

There are no routes for installing or rolling back garminctl itself; only the
desktop window can do that. See [updating garminctl](updating.md).

Sources: [route list](../tests/contracts/http_routes_test.go),
[access rules](../internal/ship/httpx/security.go),
[status mapping](../internal/ship/httpx/body.go), and each feature's `ui/http`
package under [`internal/containers`](../internal/containers).
