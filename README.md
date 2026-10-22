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

Pre-built binaries for Windows and Linux are attached to each
[release](https://github.com/DevWolk/garminctl/releases); `garminctl
version` prints the version a binary was built from. From source:

```sh
go install github.com/DevWolk/garminctl/cmd/garminctl@latest
```

`make` builds both binaries. Development targets:

```sh
make check        # golangci-lint (govet included), go test -race, govulncheck
make lint-fix     # apply the auto-fixable lint findings and formatting
```

`.golangci.yml` is the source of truth for the linters. golangci-lint and
govulncheck are pinned in `tools.mod` and run through `go tool`, so nothing
has to be installed by hand. Every `//nolint` names its linter and gives
the reason.

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
bin/garminctl app-rm "Simply Large"        # remove a Connect IQ app (finished on unplug)
bin/garminctl app-hide Archery | app-show Archery | app-pending | app-restore ~/.cache/garminctl/OUT.BIN-…
bin/garminctl autostart on          # run serve hidden at Windows sign-in (copies the Windows build to %LOCALAPPDATA%\garminctl)
bin/garminctl health extras | health history 365 | health weight 80.5
bin/garminctl hidden GARMIN/Apps/Media      # list a folder the MTP listing hides
bin/garminctl serve -autosync=false         # web UI without the USB watcher
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

## Automatic sync

`garminctl serve` watches USB. When the watch appears it connects, syncs
with Garmin Connect if you are signed in (uploads new files, writes what
Connect queued: settings, apps, workouts), checks Garmin's update service,
and shows a Windows notification with the outcome, as Garmin Express did in
the background. The web UI follows along: it switches to the watch when it
is plugged in and shows the same events as toasts. `-autosync=false` turns
the watcher off.

Storage: the watch reports its capacity and free space (PTP GetStorageInfo);
`info`, the Overview, the Music tab, the Store detail and the Updates tab
show it. `autostart on` installs a Windows Startup shortcut that runs the
Windows build of `serve` hidden at sign-in, so the watcher, sync and
notifications work the way Garmin Express did without a terminal;
`autostart off` removes it.

## Web UI

```sh
bin/garminctl serve            # http://127.0.0.1:8787
```

The API has no login, so the server only answers browser requests that come
from its own pages: a request with another `Origin`, a cross-site
`Sec-Fetch-Site`, or a `Host` that is not the loopback address on the listen
port is refused with 403. That keeps a web page open in another tab from
writing to the watch through the browser (cross-site requests, DNS
rebinding). Tools such as `curl` on the same machine are unaffected.

Health & activities also shows what else Connect knows: VO2 max, fitness
age, HRV status, personal records (linked to the activity in Connect),
earned badges, weight history and a form to log a weigh-in; ranges above
14 days come from Connect's daily-range endpoints, so a year of steps,
resting heart rate, stress, body battery and sleep loads in a few requests.
Connect IQ apps can be removed from App settings or Apps & glances (the
watch finishes the removal on unplug). Apps & glances also shows which
built-in activities, glances and apps are in your lists on the watch and
lets you hide or show them (`app-hide`, `app-show`, `app-pending` on the
CLI, `watch_set_app_hidden` for MCP): the change is queued in the watch's
app list and applied on unplug, with a copy of the list kept in
`~/.cache/garminctl`. The Overview lists queued app-list changes next to
the pending files and can cancel them.

Lists can be narrowed everywhere: Apps & glances by kind, by Shown, Hidden
or waiting-for-unplug, and ordered as on the watch, by name, by kind or
Connect IQ first; the Store by rating, number of reviews, free or paid,
installed state, whether the app fits in the watch's free space and
whether it has settings (on top of the store's own search, kinds and sort
orders); activities by sport and by date, distance, time, heart rate or
calories; files by any column; music tracks by text.

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

Tools (33): `watch_info`, `watch_list_files`, `watch_decode_fit`,
`watch_delete_file`, `watch_settings`, `watch_settings_schema`,
`watch_set_settings`, `watch_apps`, `watch_app_settings`,
`watch_app_settings_schema`, `watch_set_app_settings`, `watch_check_updates`,
`watch_set_app_hidden`, `watch_remove_app`, `watch_install_update`,
`cloud_status`, `cloud_sync`, `cloud_settings`, `cloud_set_settings` (sign in
first via the web UI or CLI; MCP never handles the password), `store_search`,
`store_app`, `activities_list`, `activity_detail`, `health_daily`,
`health_history`, `health_extras`, `health_log_weight`, `health_day`,
`workouts_list`, `workout_create`, `workout_send`, `music_list`,
`music_playlist`. Every tool carries a title, read-only/destructive and
open-world hints and a typed output schema; list results are wrapped in an
object (`entries`, `rows`, `apps`, `days`, ...).

## Layout

| Path | What |
|---|---|
| `cmd/garminctl` | CLI: `main.go` holds the command table, one file per command group (`files.go`, `settings.go`, `cloud.go`, `health.go`, ...) |
| `internal/device` | device abstraction; `wpd/` is the Windows MTP bridge, `DirFS` serves a directory |
| `internal/fit` | lossless FIT codec, embedded FIT profile 21.218, builder for new files |
| `internal/ciq` | Connect IQ serialization (`.SET` files) |
| `internal/prg` | Connect IQ `.PRG` section reader |
| `internal/ciqstore` | fetches and parses the public app-settings page (labels, options) |
| `internal/garmin` | service layer: settings view, settings-change files, apps, knowledge notes; `Lazy` is the one lazily opened watch the servers share |
| `internal/paths` | where garminctl keeps its cache (`~/.cache/garminctl`) |
| `internal/testdump` | builds a watch dump from the fixtures for tests |
| `internal/web` | HTTP API and the embedded UI |
| `internal/mcpserver` | MCP tools |
| `tools/expresswatch` | the daily Garmin Express release check behind the `express-watch` workflow |
| `docs/formats.md` | what was reverse engineered and how it fits together |
| `Makefile`, `.golangci.yml` | build and quality targets; the linter configuration |

Run the tests with `go test ./...`. The `testdata` directories hold files
pulled from the author's watch (they include personal profile data).

## Keeping up with Garmin Express

garminctl replaces Garmin Express, so a new Express release is the one
"dependency" that can make it fall behind. The `Garmin Express watch`
workflow (`.github/workflows/express-watch.yml`) runs once a day: it asks
Garmin's download server about the installer and reads the newest
release-notes thread on Garmin's forum. When either changes it records the
release in `docs/express-release.json` and opens a pull request with
Garmin's change notes. `go run ./tools/expresswatch` does the same by hand.

With a repository secret `EXPRESS_WATCH_TOKEN` (a personal access token
with the `repo` scope, or a fine-grained one with contents, pull requests,
issues and actions read/write) the pull request also triggers CI, and the
workflow starts a Copilot coding-agent task that reads the notes, decides
whether garminctl has to follow, implements what is needed and records the
assessment in `docs/express-parity.md` in a pull request of its own. Copilot
coding agent has to be enabled for the repository (Settings → Copilot).
Without the secret the pull request still opens; CI then has to be started
by hand, and Copilot is not asked.

`Update pinned tools` (`tools-update.yml`) is the matching weekly bump for
`tools.mod`, which Dependabot does not read; Dependabot itself covers
`go.mod` and the GitHub Actions pins.

## License

MIT, see [LICENSE](LICENSE).
