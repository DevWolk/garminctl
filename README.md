# garminctl

Manage a Garmin watch over USB without Garmin Express: read and change the
watch settings, edit Connect IQ watch-face settings (for example Easy+),
browse and copy files, all from a CLI, a local web UI, or an MCP server so an
AI assistant can do it from a prompt.

Tested with a vívoactive 5 (firmware 17.05) from WSL2 on Windows 11. No
.NET runtime, no drivers, no cloud account: the watch is reached through the
Windows Portable Devices API that is part of Windows.

## Build

```sh
go build -o bin/garminctl ./cmd/garminctl                      # Linux/WSL binary
GOOS=windows GOARCH=amd64 go build -o bin/garminctl.exe ./cmd/garminctl   # native Windows
```

Both builds talk to the watch the same way: they start `powershell.exe`
(from WSL through interop) with the bridge script embedded in the binary.
The first call compiles the bridge (about a second); later calls are fast.
Garmin Express may stay installed, but close it while using garminctl so the
two do not write to the watch at the same time.

## CLI

```sh
bin/garminctl devices                      # portable devices Windows sees
bin/garminctl info                         # model, unit id, firmware, pending files
bin/garminctl ls GARMIN/Activity           # browse
bin/garminctl cat -o Settings.fit GARMIN/Settings/Settings.fit
bin/garminctl decode GARMIN/Sports/WalkingWalk.fit
bin/garminctl settings                     # documented fields; add -all for everything
bin/garminctl settings set deviceSettings.backlightMode=manual userProfile.weight=79.5
bin/garminctl apps                         # installed apps, watch faces, glances
bin/garminctl app-settings easy            # Easy+ watch face settings with labels
bin/garminctl app-schema easy bar1a        # dropdown options of one setting
bin/garminctl app-settings set easy seconds=off "bar1a=Body Battery %" cust_color1=FF7A2A
bin/garminctl updates                      # firmware/data updates from Garmin + app versions vs the store
bin/garminctl updates install 006-DA094-00 # download, verify and write one update
bin/garminctl software                     # software components the watch reports
bin/garminctl activities -n 20             # activities decoded from the watch's own FIT files (cached)
bin/garminctl activity 2026-10-18-14-02-40.fit
bin/garminctl health -days 14              # daily steps, resting HR, stress, body battery, sleep (Garmin Connect)
bin/garminctl health day 2026-10-22        # intraday heart rate, stress, body battery, sleep stages
bin/garminctl workouts                     # workouts in Garmin Connect
bin/garminctl workouts create plan.json -send   # build a structured workout, save it in Connect, deliver to the watch
bin/garminctl workouts create plan.json -local  # or write the workout FIT straight to the watch
bin/garminctl music ls | music add song.mp3 | music playlist Run track1 track2
bin/garminctl store search solar face      # Connect IQ store, filtered to this watch (public catalogue)
bin/garminctl store list -type widget -sort mostPopular
bin/garminctl store info 7c993e2b-70b3-459a-bbe0-db48a89af5a8
bin/garminctl backup ~/garmin-backup       # copy the watch's storage (music left out by default)
bin/garminctl cloud login                  # sign in to Garmin Connect (asks for the password; MFA supported)
bin/garminctl cloud status
bin/garminctl cloud sync                   # upload new activities/health files, pull what Connect queued
bin/garminctl cloud settings               # device settings Garmin Connect keeps (the managedByConnect ones)
bin/garminctl cloud settings set timeFormat=time_twenty_four_hr
bin/garminctl -root /path/to/dump info     # work on a directory instead of the watch
```

Settings addresses are `message.field`, using the names of the public FIT
profile (`deviceSettings`, `userProfile`, `hrZone`, `zonesTarget`, ...).
Repeated messages take an index (`hrZone[5].highBpm=180`), array fields an
element (`deviceSettings.104[4]=8`), undocumented fields a number.

Device settings are written as a Garmin settings-change file into
`GARMIN/NewFiles`; the watch applies it when the USB session ends, so unplug
or eject the watch afterwards. Fields that Garmin Connect manages are marked
`managedByConnect`: Connect pushes its own copy back on every Express or
phone sync, so set those in Garmin Connect too.

Updates come from the same service Garmin Express uses
(`omt.garmin.com/api/device-software/v1/updates`); it answers to the watch's
`GarminDevice.xml` without a login. `updates install` downloads the file,
checks its MD5 and writes it where Garmin says (for example
`Garmin/RemoteSW/<name>.db`); the watch installs it when the USB session
ends, and firmware updates restart it. Connect IQ app updates arrive through
Garmin Connect's queue (see below) or as a sideloaded `.PRG` through the
web UI; the Updates tab reports which installed apps have a newer store
version.

Connect IQ app settings are written straight into the app's `.SET` file and
take effect immediately. Labels, dropdown options and value ranges come from
the public settings page on apps.garmin.com (cached under
`~/.cache/garminctl/appsettings/`), so dropdown choices can be given by
label.

## Health, activities, workouts, music

Activities are decoded from `GARMIN/Activity/*.fit` on the watch (session,
laps, records) and cached in `~/.cache/garminctl/activities-<unit>.json`;
the Health tab lists them and draws the GPS track on an OpenStreetMap map
with heart-rate and elevation charts. Daily health (steps, resting heart
rate, stress, body battery, sleep, calories) and intraday series come from
Garmin Connect, which keeps the history the watch itself discards after
each sync.

Workouts are described in a small JSON spec:

```json
{"name":"4x400","sport":"running","steps":[
  {"type":"warmup","duration":"10:00"},
  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},
                       {"type":"recovery","duration":"1:30"}]},
  {"type":"cooldown","duration":"5:00","target":"hr zone 2"}]}
```

Step types: warmup, cooldown, interval, recovery, rest, other. One end
condition per step: `duration` ("10:00", "90s", "1h"), `distance` ("400m",
"2km", "1mi"), `calories`, or `lap: true`. Targets: "hr 140-160",
"hr zone 3", "pace 4:30-4:45" (min/km), "speed 10-12" (km/h),
"cadence 170-180", "power 200-250". The spec is rendered either as Garmin
Connect's workout JSON (saved to the account and queued to the watch
through the device-message queue) or as a workout FIT file written straight
into `GARMIN/NewFiles`; both were verified to match the file Connect
generates. Courses are not offered because the vívoactive 5 has no course
data type.

Music: downloads made by streaming apps (YouTube Music, Spotify, …) are
DRM-protected and live in the hidden app area; only their cache size is
known and shown as "Streaming apps". Your own audio files (mp3, m4a, aac, wav, flac) are written to
`Music/<Artist>/<Album>/<Title>` from their ID3 tags (Podcasts and
Audiobooks folders likewise); playlists are `.m3u` files in `Music/`.

## Connect IQ store

The store website's own JSON API answers without a login, so the Store tab,
`store` commands and `store_search`/`store_app` MCP tools browse and search
the catalogue filtered to this watch, with ratings, downloads, price, size
for this device, permissions, description, what's new and reviews, and mark
what is installed or has a newer version. Installing goes through Garmin
Connect's queue: the store website no longer takes installs, so queue the
app in the Connect IQ phone app while the phone cannot reach the watch
(Bluetooth off), then `cloud sync` pulls the queued `.PRG` and writes it to
the watch. This watch (like the fēnix 8) hides its app folder from the
ordinary MTP listing; the tool reaches it through Garmin's vendor PTP
operation and raw SendObject, verified on 2026-10-22 with a watch face that
the watch registered on unplug. Sideloaded `.PRG` files take the same path.

## Garmin Connect (cloud)

Sign in once (`cloud login` or the Garmin Connect tab of the web UI). The
sign-in is the Garmin Connect Mobile flow: Garmin SSO, an OAuth 1.0 token,
exchanged for a bearer. Only the tokens are stored, in
`~/.config/garminctl/garmin-connect.json` (mode 0600); the password is never
written anywhere. With a session the tool does what Garmin Express did over
USB:

- `cloud sync` uploads every recorded file Garmin has an upload endpoint
  for (activities, daily monitoring, sleep, metrics, HRV, …) that this
  computer has not sent before, and recognises files the phone already
  synced (Garmin answers "duplicate"). Then it pulls Connect's device-message
  queue: settings-change files, Connect IQ app installs and updates queued
  from the store website or by Connect's auto-update, workouts, courses, golf
  clubs, personal records; each is written where the watch expects it and
  acknowledged. Nothing is deleted from the watch.
- `cloud settings` shows the device settings record Connect keeps; `cloud
  settings set` changes it and fetches the settings file Connect generates
  into `GARMIN/NewFiles`. This is the way to change a `managedByConnect`
  field so that Connect stops reverting it.
- Types that need a privacy consent in Connect (HRV, skin temperature, sleep
  disruptions, …) are skipped with a note until the consent is given.

## Web UI

```sh
bin/garminctl serve            # http://127.0.0.1:8787
```

Sections: Overview (the watch with its Easy+ face drawn from the settings
file as it is right now, today's steps, resting heart rate, sleep, body
battery and calories from Garmin Connect, pending NewFiles, latest
activities), Watch settings (grouped with a jump list, filterable, inline
editing with dropdowns, one review step before writing), Apps & glances (the watch's whole app registry: built-in
activities, glances and apps plus Connect IQ installs, with a filter by
kind and links to each app's settings and store page), App settings (edit any Connect IQ app's settings with the
store's labels; Easy+ gets a live emulator in `internal/web/ui/face.js`
that redraws from the pending values, switches between screen 1 and 2,
previews always-on modes and jumps to a setting when you click a field),
Updates (watch software and data from Garmin's update service with release
notes and one-click install, Connect IQ app versions against the store,
`.PRG` sideload, installed components), Files (browse, download, decode FIT,
upload, delete), Garmin Connect (sign in, sync, Connect's queue for the
watch, the settings Connect keeps). The sidebar's "Garmin online" group opens Garmin Connect,
its device page (where Connect-managed settings live), Garmin Coach, the
Connect IQ store, the watch's manual and support page, Garmin's status page
and garmin.com, each in its own browser tab that is reused on the next click
(Garmin Connect refuses to be embedded). The app editor links to the
selected app's store page. The UI is plain HTML, CSS and JS in
`internal/web/ui/` (Manrope and JetBrains Mono from Google Fonts with system
fallbacks; dark and light follow the OS). The JSON API behind it is in
`internal/web/server.go`.

## MCP server

```sh
bin/garminctl mcp              # stdio transport
```

Claude Code configuration (`.mcp.json` or `claude mcp add`):

```json
{ "mcpServers": { "garmin": { "command": "/home/ubuntu/projects/garmin/bin/garminctl", "args": ["mcp"] } } }
```

Tools: `watch_info`, `watch_list_files`, `watch_decode_fit`,
`watch_delete_file`, `watch_settings`, `watch_settings_schema`,
`watch_set_settings`, `watch_apps`, `watch_app_settings`,
`watch_app_settings_schema`, `watch_set_app_settings`, `watch_check_updates`,
`watch_install_update`, `cloud_status`, `cloud_sync`, `cloud_settings`,
`cloud_set_settings` (sign in first via the web UI or CLI; MCP never handles
the password), `store_search`, `store_app`, `activities_list`,
`activity_detail`, `health_daily`, `health_day`, `workouts_list`,
`workout_create`, `workout_send`, `music_list`, `music_playlist`.

## Layout

| Path | What |
|---|---|
| `cmd/garminctl` | CLI entry point |
| `internal/device` | device abstraction; `wpd/` is the Windows MTP bridge, `DirFS` serves a directory |
| `internal/fit` | lossless FIT codec, embedded FIT profile 21.218, builder for new files |
| `internal/ciq` | Connect IQ serialization (`.SET` files) |
| `internal/prg` | Connect IQ `.PRG` section reader |
| `internal/ciqstore` | fetches and parses the public app-settings page (labels, options) |
| `internal/garmin` | service layer: settings view, settings-change files, apps, knowledge notes |
| `internal/web` | HTTP API and the embedded UI |
| `internal/mcpserver` | MCP tools |
| `docs/formats.md` | what was reverse engineered and how it fits together |

Run the tests with `go test ./...`. The `testdata` directories hold files
pulled from the author's watch (they include personal profile data).
