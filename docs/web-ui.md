# Web UI

The [desktop app](desktop.md) includes this interface. For browser access, run:

```sh
garminctl serve
```

Open `http://127.0.0.1:8787`. The default server binds to loopback and has no
API login. Host/port checks reject unexpected hostnames; Origin and browser
fetch checks reject cross-origin API calls. Local clients can access the API.

The server detects the USB watch automatically. **Connect watch** opens it
manually, and **Reload** rereads device data. See [sync](cloud-sync.md) for
automatic cloud actions and [CLI options](cli.md#servers) for directory mode.

## Tabs

| Tab | Actions and data |
| --- | --- |
| Overview | Connected device, storage, pending files and app-list changes, today's health data, recent activities, and a watch-face preview when available. |
| Watch settings | Filter grouped fields, edit values, and review changes before applying. |
| App settings | Edit Connect IQ settings using store labels; Easy+ has a live preview. |
| Apps & glances | Browse installed entries, hide/show them, open settings or store pages, and inspect pending changes. |
| Health & activities | View recorded activities, GPS maps and charts, daily/intraday health, records, badges, weight history, and weigh-in logging. |
| Workouts | Preview a JSON specification, save and send it through Connect, write it directly to the watch, or send/delete Connect workouts. |
| Music | Upload/delete audio, create playlists, and view streaming-app cache size. |
| Store | Browse Connect IQ listings, details, permissions and reviews; filter by the connected device. |
| Updates | Check/install watch software and data, compare app versions, sideload `.PRG` files, and inspect installed components. |
| Application | Backend version and GitHub Releases link; the native desktop also shows local versions, installation type and saved release history with explicit update checks, plus verified package preparation/progress/cancel in builds with a trusted key. |
| Garmin Connect | Sign in, sync, inspect queued files, and edit Connect-managed settings. |
| Files | Browse, upload, download, delete, and decode FIT files; download decoded JSON. |
| AI & MCP | Copy connection instructions and view assistant sessions and tool calls. |

Activities come from FIT files on the watch; daily health and extras require
Garmin Connect sign-in. Lists provide filters and sorting. FIT decoding groups
messages by name and pages large groups.

The URL preserves the selected tab and item, such as `/files/GARMIN/Apps` or
`/store?q=solar`. Reload, Back, and pasted links return to that view. External
links open browser tabs, or the default browser from the native app.

See [CLI settings](cli.md#settings-and-apps) for when watch and app edits take effect.

## Browser app

A browser that supports installed web apps can install garminctl from the
address bar or browser menu. The server must still run when the browser app
opens. Windows users can configure its [background server](cli.md#servers).

Sources: [interface](../internal/ship/web/ui/index.html),
[routes and request checks](../internal/ship/web/server.go).
