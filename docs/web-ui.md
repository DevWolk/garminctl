# Web UI

The web UI is garminctl's interface in a browser. The
[desktop app](desktop.md) shows the same interface in its own window.

## Open it

1. Run the server:

   ```sh
   garminctl serve
   ```

2. Open `http://127.0.0.1:8787`.
3. Connect the watch by USB. The server detects it. **Connect watch** opens it
   by hand, and **Reload** rereads the watch.

To read a directory copy instead of a watch, or to change the address, see the
[server options](cli.md#servers).

## Change something

1. In **Watch settings**, **App settings** or the settings in **Garmin
   Connect**, edit the values.
2. Choose **Review and apply**. A dialog lists the changes; confirm it to save
   them. **Discard** drops them.
3. Unplug the watch. Watch settings and app-list changes take effect then;
   Connect IQ app settings are saved at once. See
   [when changes take effect](cli.md#when-changes-take-effect).

Edits that you have not saved with **Review and apply** are kept only in the
page. **Reload** asks before it drops them, and keeps them if the watch cannot
be reread. Unplugging the watch drops them.

## Tabs

| Tab | What you can do |
| --- | --- |
| Overview | See the connected watch, storage, pending files and app-list changes, today's health data, recent activities, and a watch-face preview when one is available. |
| Watch settings | Filter the grouped fields, edit values, and review the changes before applying them. |
| App settings | Edit Connect IQ app settings with the labels from the store. The Easy+ watch face has a live preview. |
| Apps & glances | Browse what is installed, hide or show entries, remove Connect IQ apps, open their settings or store pages, and see pending changes. |
| Health & activities | View recorded activities with GPS maps and charts, daily and intraday health, records, badges and weight history, and log a weigh-in. |
| Workouts | Preview a workout written as JSON, save it and send it through Garmin Connect, write it straight to the watch, or send and delete Connect workouts. |
| Music | Upload and delete audio, create playlists, and see how much streaming apps have cached. |
| Store | Browse Connect IQ listings, details, permissions and reviews, filtered for the connected watch. |
| Updates | Check for and install watch software and data, compare app versions, sideload `.PRG` files, and list installed components. |
| Application | See the server's version and a link to GitHub Releases. In the desktop app: local versions, installation type, release history, and [updating garminctl](updating.md#update-from-the-app). |
| Garmin Connect | Sign in, sync, see queued files, and edit the settings that Connect manages. |
| Files | Browse, upload, download and delete files; decode FIT files and download the result as JSON. |
| AI & MCP | Copy the connection instructions for an assistant and see its sessions and tool calls. |

Below the tabs, **Garmin online** links to Garmin's own websites.

- Activities come from files on the watch. Daily health, records, badges and
  weight need [Garmin Connect sign-in](cloud-sync.md).
- Lists can be filtered and sorted. A decoded FIT file is grouped by message
  name, and large groups are split into pages.
- The address keeps the open tab and item, such as `/files/GARMIN/Apps` or
  `/store?q=solar`. Reload, Back and pasted links return to that view.
- Links to other websites open in a new browser tab, or in the default browser
  from the desktop app.

## Security

The server has no login. By default it listens on `127.0.0.1`, which only this
computer can reach.

- Programs on this computer can call the server.
- Other websites cannot. The server rejects requests whose host name is
  unexpected, and requests that a page from another site started.
- Do not start the server on another address unless you trust everyone who can
  reach it: they could change the watch and use your Garmin Connect sign-in.

The exact checks are in the [HTTP API reference](http-api.md#access-rules).

## Install it as a browser app

A browser that supports installed web apps can install garminctl from the
address bar or the browser menu. The server must be running when the browser
app opens. On Windows, `garminctl autostart on` starts the server when you log
in; see [start the server when you log in to Windows](cloud-sync.md#start-the-server-when-you-log-in-to-windows).

Sources: [interface](../internal/ship/web/ui/index.html),
[page routes](../internal/ship/web/server.go),
[request checks](../internal/ship/httpx/security.go).
