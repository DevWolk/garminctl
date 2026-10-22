# Web UI

Start the server at `http://127.0.0.1:8787`:

```sh
garminctl serve
```

The API has no login. The server refuses browser requests with a different
`Origin`, a cross-site `Sec-Fetch-Site`, or a `Host` other than the loopback
address and listen port. This prevents another site from writing to the watch
through the browser, including through cross-site requests or DNS rebinding.
Tools such as `curl` on the same machine are unaffected.

## Tabs

- **Overview:** watch status, Easy+ face rendered from its current settings,
  today's Connect health data, pending NewFiles, queued app-list changes, and
  recent activities. Queued app-list changes can be cancelled here.
- **Watch settings:** grouped fields, jump list, filtering, dropdown editing,
  and a review step before writing.
- **Apps & glances:** the watch's app registry, including built-in activities,
  glances and apps, and Connect IQ installs. Filter by kind; open app settings
  or its store page; hide or show entries. The watch applies registry changes
  on unplug. A registry copy is kept in `~/.cache/garminctl`.
- **App settings:** edit Connect IQ settings using store labels. Easy+ has a
  live emulator that redraws pending values, switches screens, previews
  always-on modes, and jumps to a setting when a field is clicked.
- **Updates:** Garmin software and data updates with release notes and
  one-click install, Connect IQ app versions against the store, `.PRG`
  sideloading, and installed components.
- **Files:** browse, download, decode FIT, upload, and delete.
- **Health & activities:** activity tracks on an OpenStreetMap map with heart
  rate and elevation charts; health history, extras, records, badges, weight
  history, and weigh-in logging.
- **Music:** music files and streaming-app cache size.
- **Store:** Connect IQ catalogue and app details.
- **Garmin Connect:** sign-in, sync, Connect's device-message queue, and
  Connect-managed settings.

Health extras include VO2 max, fitness age, HRV status, personal records linked
to Connect activities, earned badges, and weight history. Ranges longer than
14 days use Connect's daily-range endpoints.

Lists can be filtered and sorted: apps and glances by kind and visibility, and
by watch order, name, kind, or Connect IQ first; store apps by rating, review
count, free/paid, installed status, free-space fit, or whether they have
settings; activities by sport or date, distance, time, heart rate, or
calories; files by any column; and music tracks by text.

The sidebar links to Garmin Connect, its device page, Garmin Coach, the Connect
IQ store, the watch manual and support page, Garmin's status page, and
garmin.com. Each opens in a browser tab reused on the next click; Garmin
Connect refuses embedding. The app editor links to the selected app's store
page. The URL carries the selected tab and item (for example
`/apps/EASY+`, `/files/GARMIN/Apps`, `/store?q=solar`) so reload, Back, or a
pasted link returns to the same view.
