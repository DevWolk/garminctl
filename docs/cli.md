# CLI reference

## Installation

Download a Windows or Linux CLI archive (amd64 or arm64) from
[GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest), unpack it,
and put `garminctl.exe` or `garminctl` on `PATH`. `checksums.txt` contains SHA-256
sums. These portable builds support Windows/WSL and directory dumps; see
[USB access](desktop.md#usb-access) for native Linux requirements.

With Go, install the CLI from source:

```sh
go install github.com/DevWolk/garminctl/cmd/garminctl@latest
```

Native Linux USB builds require CGO, `pkg-config`, and libusb development headers
(`libusb-1.0-0-dev` on Ubuntu). The [desktop packages](desktop.md) include a CLI.

## Global options

```sh
garminctl [global flags] COMMAND [args]
garminctl -h
```

| Flag | Default | Usage |
| --- | --- | --- |
| `-device ID` | Automatic selection | Select an ID from `devices`. |
| `-root DIR` | `GARMINCTL_ROOT`, otherwise USB | Use a mounted watch or directory dump. |
| `-json` | `false` | Return JSON where supported. |

Windows/WSL lists Windows portable devices and automatically selects the first
Garmin. Native Linux lists Garmin USB devices and requires `-device ID` if
several are present. IDs belong to their transport. Directory mode overrides USB
selection; use `garminctl -root ./watch-dump info` with a directory containing
`GARMIN/` for offline data.

Standalone watch commands open their own connection. HTTP MCP and the UI share
their server's connection; see [MCP](mcp.md). Exit codes are `0` for success,
`1` for failure, `2` for usage errors, and `130` for interruption.

## Files and device information

```text
version                         Print the build version.
devices                         List devices available through the host transport.
info                            Show device, firmware, storage, and pending files.
ls [PATH]                       List a directory; default is the storage root.
cat [-o FILE] PATH              Read text or save a binary file locally.
put FILE PATH                   Write a local file to the watch.
rm PATH                         Delete a watch file.
mkdir PATH                      Create a watch directory.
decode PATH                     Decode a watch FIT file.
backup DIR [-skip FOLDERS]      Copy storage; skips Music,Podcasts,Audiobooks by default.
hidden [-write-test] PATH       List hidden entries through WPD.
mtp OPCODE [PARAM...]           Run a raw WPD read operation; numbers are hexadecimal.
```

`hidden -write-test` writes, reads, and deletes a temporary file. The diagnostic
commands `hidden` and `mtp` require WPD.

## Settings and apps

```text
settings [-all]                 Show documented fields in text; -all includes unknown fields.
settings set M.F=V [...]        Queue device settings changes.
apps                            List installed apps, faces, glances, and activities.
app-settings APP                Read a Connect IQ app's settings.
app-settings set APP K=V [...]  Write app settings; option labels are accepted.
app-schema APP [KEY]            Show setting labels, ranges, and choices.
app-hide APP | app-show APP     Queue an app-list visibility change.
app-pending                     Show queued app-list changes.
app-restore FILE                Queue a saved app registry.
app-rm APP                      Remove a Connect IQ app.
updates                         Check software/data updates and app versions.
updates install PART            Download, verify, and write one software/data update.
software                        List installed software components.
```

Use an app name or file ID from `apps`. Settings addresses accept message names,
message indexes, numeric fields, and array indexes:

```sh
garminctl settings set deviceSettings.backlightMode=manual
garminctl settings set 'hrZone[5].highBpm=180' 'deviceSettings.104[4]=8'
```

Device settings write change files to `GARMIN/NewFiles`; they never rewrite
`GARMIN/Settings/Settings.fit`. `app-hide` and `app-show` write
`GARMIN/Apps/IN.BIN` only after writing and reading back a private registry
backup in the [cache directory](desktop.md#data-updates-and-uninstall). If the
backup fails, the command returns an error without changing `IN.BIN`.
`app-restore` writes the given file to `IN.BIN` without a backup.
The watch applies queued changes and completes app removal after USB
disconnection. App `.SET` files are rewritten immediately. Fields marked `managedByConnect`
also need changing in [Connect settings](cloud-sync.md#manual-sync).

Updates check the MD5 when Garmin supplies one, then write Garmin's indicated
path. The watch applies them after USB disconnection. Connect IQ app packages
arrive through Connect's queue or `.PRG` sideloading in the [UI](web-ui.md).

`updates` retains successful results when another check fails. In `-json` mode,
the existing `software` and `apps` fields remain; failures add an `errors` object
with `software` and/or `apps` messages. Per-app errors also remain on their app
rows. A failed check returns exit status 1 in text and JSON modes; an interrupted
check returns 130. Successful JSON output omits `errors`.

App settings schemas are cached locally. If the cache cannot be written, garminctl
logs a warning and continues using the fetched schema; a later run may fetch it
again.

## Health, workouts, music, and store

```text
activities [-n N] [-wait]       List activities; N defaults to 30; -wait finishes decoding.
activity FILE                   Show an activity's laps and sampled GPS track.
health [-days N]                Read daily Connect health; N defaults to 14.
health day YYYY-MM-DD           Read intraday health.
health history [DAYS]           Read daily history; defaults to 90 days.
health extras                   Read fitness metrics, records, badges, and weight.
health weight KG                Log a weigh-in in Connect.
workouts                        List Connect workouts.
workouts create FILE.json [-send] [-local]
workouts send ID                Queue and fetch a Connect workout.
music [ls]                      List tracks, playlists, and streaming-app cache sizes.
music add [-folder Music] FILE...
music rm PATH...                Delete tracks.
music playlist NAME TRACK...    Write a playlist.
store search WORDS [-type T] [-sort S] [-page N]
store list [-type T] [-sort S] [-page N]
store info APP_ID               Show app details and reviews.
```

`health -days N` uses the history API when N exceeds 14, matching the web API.
History supports up to 366 days; larger requests use that limit. Short ranges
retain the daily API and the existing text/JSON output format. History rows
name the fields that API does not provide in `unavailable` (calories, min/max
heart rate, floors, intensity minutes, active time); the text output prints
`-` for them. A day whose range request failed carries `error` and is not
cached, so a later call fetches it again.

Activities are decoded locally and cached. Health and Connect workouts require
sign-in. `workouts create` saves in Connect by default; `-send` also fetches the
queued workout, while `-local` writes FIT directly to `GARMIN/NewFiles`.

A workout JSON specification contains a name, sport, and steps:

```json
{"name":"Intervals","sport":"running","steps":[
  {"type":"warmup","duration":"10:00"},
  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},
                       {"type":"recovery","duration":"1:30"}]},
  {"type":"cooldown","duration":"5:00"}]}
```

Each step ends by `duration` (`10:00`, `90s`, `1h`), `distance` (`400m`, `2km`,
`1mi`), `calories`, or `lap: true`. Types include warmup, cooldown, interval,
recovery, rest, main, and other. Targets accept `hr 140-160`, `hr zone 3`,
`pace 4:30-4:45` (min/km), `speed 10-12` (km/h), `cadence 170-180`, or `power 200-250`.

Music accepts mp3, m4a, aac, adts, wav, and flac. `-folder` defaults to `Music`; it also
accepts `Podcasts` or `Audiobooks`. Music paths use available ID3 tags; playlists
use `.m3u`. Streaming downloads expose cache sizes only.

The store needs no login and filters by the connected device. Types are
`watchface`, `watch-app`, `widget`, `datafield`, or `audio-content-provider-app`.
Listings default to `watchface`. Listing sort values are `mostPopular`,
`highestRated`, `mostRecent`, or `trending`; an omitted sort uses the store's
default. Search ignores sorting. `-page` defaults to `0`.

## Servers

```text
desktop                         Launch the installed app; accepts no arguments.
serve [-addr IP:PORT] [-autosync=false]
mcp                             Run an independent stdio MCP server.
autostart [on|off|status]        Manage Windows background-server startup; defaults to status.
```

`serve` starts the UI, HTTP API, and `/mcp` at `127.0.0.1:8787` by default.
`-autosync` defaults to `true`; setting it to `false` disables USB watching and
automatic sync. `-root` also disables watching. The desktop executable has
[separate launch flags](desktop.md#launch-options).

## Cloud commands

```text
cloud login [-email ADDRESS]    Sign in; credentials can also come from environment variables.
cloud mfa CODE                  Finish a pending verification from cloud login.
cloud status | cloud logout     Inspect or remove the saved session.
cloud sync [-no-upload] [-no-download]
cloud messages                  Inspect queued files.
cloud settings                  Read Connect-managed settings.
cloud settings set K=V [...]    Update Connect settings and fetch its generated file.
cloud get|post PATH [BODY]      Make a raw bearer-token API request.
cloud get1|post1 URL [BODY]      Make a raw OAuth1 request to an absolute URL.
```

Sync defaults to both directions. Account commands (`login`, `mfa`, `logout`)
need no watch. Complete MFA in the interactive `cloud login` prompt or use
`cloud mfa CODE` in a later invocation. See [Garmin Connect](cloud-sync.md) for
verification lifetime, account storage and sync behavior.

Sources: [command help and global flags](../cmd/garminctl/main.go),
[command implementations](../cmd/garminctl), [workout schema](../internal/garmin/workouts.go).
