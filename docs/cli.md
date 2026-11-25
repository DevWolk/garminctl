# CLI

`garminctl` runs one command at a time. Most commands open the watch, do their
work and exit; `serve` and `mcp` keep running until you stop them.

```sh
garminctl devices          # which watches does this computer see?
garminctl info             # model, software, storage, pending changes
garminctl settings         # watch settings
garminctl -h               # every command
```

## Install

The [desktop packages](desktop.md#install) include the CLI. To install only
the CLI:

1. Download the archive for your system from
   [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest).
   There are Windows and Linux archives for amd64 and arm64. `checksums.txt`
   holds their SHA-256 sums.
2. Unpack it.
3. Put `garminctl.exe` or `garminctl` from the unpacked folder on your `PATH`.

These portable builds reach the watch on Windows and WSL, and read
[directory copies](#global-options) everywhere. They cannot use USB directly on
Linux; see [USB access](desktop.md#usb-access).

Windows Setup installs the CLI but does not put it on `PATH`; see
[run the CLI on Windows](desktop.md#run-the-cli-on-windows).

With Go, install from source:

```sh
go install github.com/DevWolk/garminctl/cmd/garminctl@latest
```

A source build with native Linux USB needs CGO, `pkg-config` and the libusb
development headers (`libusb-1.0-0-dev` on Ubuntu).

To replace an installed CLI with a newer one, see
[update a standalone CLI](updating.md#update-a-standalone-cli).

## Global options

```sh
garminctl [global flags] COMMAND [args]
```

| Flag | Default | Use |
| --- | --- | --- |
| `-device ID` | Chosen automatically | Use the watch with this ID from `garminctl devices`. |
| `-root DIR` | `GARMINCTL_ROOT`, otherwise USB | Read a directory instead of USB: a mounted watch or a copy that contains `GARMIN/`. |
| `-json` | Off | Print JSON, where the command supports it. |

Global flags come before the command: `garminctl -root ./watch-dump info`.

**Which watch is used.** On Windows and WSL, garminctl lists Windows portable
devices and picks the first Garmin. On native Linux it lists Garmin USB devices;
with several connected, `-device ID` is required. An ID belongs to the
transport that listed it. `-root` takes precedence over USB.

**Offline work.** With `-root DIR`, commands read and write the directory
instead of a watch. A change that the watch would apply after unplugging, such
as a settings change, is only written there as a pending file. Storage size and
the `hidden` and `mtp` commands are unavailable, and `devices` still lists USB
devices.

**Connections.** A standalone command opens its own connection. The web UI and
HTTP MCP share their server's connection instead; see [MCP](mcp.md).

**Exit status.** `0` success, `1` failure, `2` usage error, `130` interrupted.

## When changes take effect

| Change | Takes effect |
| --- | --- |
| `settings set` | After you unplug the watch. |
| `app-hide`, `app-show`, `app-restore` | After you unplug the watch. |
| `app-rm` | Completes after you unplug the watch. |
| `app-settings set` | Immediately. |
| `updates install` | The watch installs the update after you unplug it. |
| `workouts create -local`, `workouts send`, `cloud sync` downloads | After you unplug the watch. |
| `put`, `rm`, `mkdir`, `music add`, `music rm`, `music playlist` | The file is written or deleted immediately. |

How garminctl writes each change:

- **Watch settings** go to a change file in `GARMIN/NewFiles`. garminctl never
  rewrites `GARMIN/Settings/Settings.fit`.
- **`app-hide` and `app-show`** write `GARMIN/Apps/IN.BIN`, but only after
  writing and reading back a private copy of `OUT.BIN` and any pending `IN.BIN`
  in the [cache directory](desktop.md#safety-copies-of-watch-files). If the
  copy fails, or a pending `IN.BIN` cannot be read for a reason other than its
  absence, the command returns an error without changing `IN.BIN`.
- **`app-restore`** writes the given file to `IN.BIN` without a copy.
- **`app-settings set`** rewrites the app's `.SET` file after a private copy of
  the current file. If that copy fails, the file is not changed.

Garmin Connect keeps its own copy of some settings and sends it to the watch
again on a later sync. Fields marked `managedByConnect` therefore also need
changing with [`cloud settings set`](#garmin-connect).

## Files and watch information

```text
version                         Print the build version.
devices                         List the watches this computer sees.
info                            Show the watch, software, storage and pending files.
ls [PATH]                       List a directory; the default is the storage root.
cat [-o FILE] PATH              Print a text file, or save a file with -o.
put FILE PATH                   Write a local file to the watch.
rm PATH                         Delete a watch file.
mkdir PATH                      Create a watch directory.
decode PATH                     Decode a FIT file on the watch.
backup DIR [-skip FOLDERS]      Copy the watch's storage to a directory.
hidden [-write-test] PATH       List hidden entries.
mtp OPCODE [PARAM...]           Run a raw read operation; numbers are hexadecimal.
```

```sh
garminctl ls GARMIN/Activity
garminctl cat -o settings.fit GARMIN/Settings/Settings.fit
garminctl -json decode GARMIN/Settings/Settings.fit
garminctl backup ./watch-backup
```

- `backup` skips `Music`, `Podcasts` and `Audiobooks` unless you change
  `-skip`. The copy keeps the watch's folder layout, so `-root DIR` can read it
  later. It is not a complete image of the watch: entries that an ordinary
  listing does not show are not copied, a file that cannot be read is reported
  and skipped, and file times are not kept.
- `hidden` and `mtp` are diagnostic commands that need WPD, so they work on
  Windows and WSL only. `hidden -write-test` writes, reads and deletes a
  temporary file.

## Settings and apps

```text
settings [-all]                 Show watch settings; -all includes undocumented fields.
settings set M.F=V [...]        Queue watch settings changes.
apps                            List installed apps, watch faces, glances and activities.
app-settings APP                Read a Connect IQ app's settings.
app-settings set APP K=V [...]  Change app settings; option labels are accepted.
app-schema APP [KEY]            Show setting labels, ranges and choices.
app-hide APP | app-show APP     Queue an app-list visibility change.
app-pending                     Show queued app-list changes.
app-restore FILE                Queue a saved app registry.
app-rm APP                      Remove a Connect IQ app.
updates                         Check for watch software, data and app updates.
updates install PART            Download, verify and write one software or data update.
software                        List installed software components.
```

### Watch settings

A setting is addressed as `message.field=value`. Names come from
`garminctl settings`. Message indexes, numeric fields and array indexes are
accepted too:

```sh
garminctl settings set deviceSettings.backlightMode=manual
garminctl settings set 'hrZone[5].highBpm=180' 'deviceSettings.104[4]=8'
```

### Apps

Use an app name or file ID from `garminctl apps`. A name, file ID or store ID
that matches more than one app is refused.

The app, glance and activity entries of one app share name and IDs. `app-hide`
and `app-show` therefore also accept `KIND:ID`, with the kind and store ID from
`apps`, for example `glance:<id>`. A reference that matches several entries is
refused, and the error lists their `KIND:ID` references.

App settings labels and choices come from the Connect IQ store and are cached
locally. If the cache cannot be written, garminctl logs a warning and uses the
fetched labels; a later run may fetch them again.

New Connect IQ apps arrive through [Garmin Connect's queue](cloud-sync.md#manual-sync)
or by sideloading a `.PRG` file in the [web UI](web-ui.md).

### Watch software updates

`updates install` checks the download's MD5 when Garmin supplies one, then
writes the file to the path Garmin names.

`updates` keeps the results that succeeded when another check fails:

- In `-json` mode, the `software` and `apps` fields remain, and failures add an
  `errors` object with `software` and/or `apps` messages. Errors for a single
  app stay on that app's row. Successful output has no `errors`.
- A failed check exits with status 1 in text and JSON modes; an interrupted
  check exits with 130.
- `updates -h` and malformed arguments print usage before anything is sent to
  the update service or the store.

## Health and activities

```text
activities [-n N] [-wait]       List activities; N defaults to 30; -wait finishes decoding.
activity FILE                   Show an activity's laps and sampled GPS track.
health [-days N]                Read daily health from Garmin Connect; N defaults to 14.
health day YYYY-MM-DD           Read intraday health.
health history [DAYS]           Read daily history; the default is 90 days.
health extras                   Read fitness metrics, records, badges and weight.
health weight KG                Log a weigh-in in Garmin Connect.
```

Activities are decoded from the watch's files and cached; they need no
sign-in. The `health` commands read Garmin Connect and need
[sign-in](#garmin-connect).

Details of the `health` ranges:

- `health -days N` uses the history data when N is more than 14, as the web
  UI does. History covers up to 366 days; a larger request uses that limit.
- History rows name the fields that Garmin's history data does not provide in
  `unavailable`: calories, minimum and maximum heart rate, floors, intensity
  minutes and active time. Text output prints `-` for them.
- When one of the range requests fails, the days that still have data add its
  fields to `unavailable`. A day without any data carries `error`. Such days
  are not cached, so a later call fetches them again.
- A day for which Connect has not reported every value yet, because the watch
  has not synced it, is cached only once a later day has data.

## Workouts

```text
workouts                        List Garmin Connect workouts.
workouts create FILE.json [-send] [-local]
workouts send ID                Queue a Connect workout and fetch it to the watch.
```

| Command | Result |
| --- | --- |
| `workouts create FILE.json` | Saves the workout in Garmin Connect. |
| `workouts create FILE.json -send` | Saves it in Connect and fetches the queued workout to the watch. |
| `workouts create FILE.json -local` | Writes the workout to `GARMIN/NewFiles` without Connect. |

Connect workouts need [sign-in](#garmin-connect).

### Workout specification

A workout is a JSON file with a name, a sport and steps:

```json
{"name":"Intervals","sport":"running","steps":[
  {"type":"warmup","duration":"10:00"},
  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},
                       {"type":"recovery","duration":"1:30"}]},
  {"type":"cooldown","duration":"5:00"}]}
```

| Part | Values |
| --- | --- |
| Step `type` | `warmup`, `cooldown`, `interval`, `recovery`, `rest`, `main`, `other` |
| How a step ends | `duration` (`10:00`, `90s`, `1h`), `distance` (`400m`, `2km`, `1mi`), `calories`, or `lap: true` |
| `target` | `hr 140-160`, `hr zone 3`, `pace 4:30-4:45` (min/km), `speed 10-12` (km/h), `cadence 170-180`, `power 200-250` |
| Repeats | `{"repeat": N, "steps": [...]}` |

## Music

```text
music [ls]                      List tracks, playlists and streaming-app cache sizes.
music add [-folder Music] FILE...
music rm PATH...                Delete tracks.
music playlist NAME TRACK...    Write a playlist.
```

- Formats: mp3, m4a, aac, adts, wav and flac.
- `-folder` is `Music` by default; `Podcasts` and `Audiobooks` are accepted.
- `music add` refuses a file whose target is already on the watch. Remove it
  first to replace it.
- Track paths use the file's ID3 tags when it has them. Playlists are `.m3u`
  files.
- For streaming apps, garminctl shows cache sizes only.

## Store

```text
store search WORDS [-type T] [-sort S] [-page N]
store list [-type T] [-sort S] [-page N]
store info APP_ID               Show app details and reviews.
```

The store needs no sign-in. Results are filtered for the connected watch.

| Flag | Values |
| --- | --- |
| `-type` | `watchface`, `watch-app`, `widget`, `datafield`, `audio-content-provider-app`. `store list` defaults to `watchface`. |
| `-sort` | `mostPopular`, `highestRated`, `mostRecent`, `trending`. Without it, the store's own order is used. `store search` ignores it. |
| `-page` | Page number, from `0`. |

## Garmin Connect

```text
cloud login [-email ADDRESS]    Sign in; credentials can also come from environment variables.
cloud mfa CODE                  Finish a sign-in that asked for a verification code.
cloud status | cloud logout     Show the account status, or sign out.
cloud sync [-no-upload] [-no-download]
cloud messages                  Show the files Garmin Connect has queued for the watch.
cloud settings                  Read the watch settings that Connect keeps.
cloud settings set K=V [...]    Change Connect settings and fetch the file Connect generates.
cloud get|post PATH [BODY]      Make a raw API request with the saved sign-in.
cloud get1|post1 URL [BODY]     Make a raw OAuth1 request to an absolute URL.
```

- `cloud sync` runs both directions unless you pass `-no-upload` or
  `-no-download`.
- `login`, `mfa` and `logout` need no watch.
- Enter the verification code at the `cloud login` prompt, or run
  `cloud mfa CODE` afterwards.

[Garmin Connect and sync](cloud-sync.md) explains sign-in, how long a
verification stays valid, where the sign-in is stored and what sync does.

## Servers

```text
desktop                         Open the installed desktop app; passes on -root and -device.
serve [-addr IP:PORT] [-autosync=false]
mcp                             Run a separate MCP server on stdio.
autostart [on|off|status]       Manage the background server at Windows sign-in; the default is status.
```

`serve` starts the [web UI](web-ui.md), the [HTTP API](http-api.md) and the
[MCP endpoint](mcp.md) `/mcp`, at `127.0.0.1:8787` by default.

| Flag | Default | Use |
| --- | --- | --- |
| `-addr IP:PORT` | `127.0.0.1:8787` | Listen address. Keep it on loopback: the server has [no login](web-ui.md#security). |
| `-autosync` | `true` | `-autosync=false` turns off USB detection and automatic sync. `-root` turns them off too. |

`autostart on` works on Windows and from WSL; see
[automatic USB sync](cloud-sync.md#automatic-usb-sync). The desktop executable
has its own [launch options](desktop.md#launch-options).

## Application recovery

These commands inspect what garminctl saved for its own updates. They need
neither a watch nor a running app, and are described in
[recovery commands](updating.md#recovery-commands).

```text
application status
application recovery list
application recovery export ID
application recovery remove-package ID
application recovery snapshots
application recovery inspect ID
application recovery remove-snapshot ID
```

Sources: [command help and global flags](../internal/app/cli/cli.go),
[command dispatch](../internal/app/cli), each feature's `ui/cli` package under
[`internal/containers`](../internal/containers),
[workout schema](../internal/containers/workouts/codec.go).
