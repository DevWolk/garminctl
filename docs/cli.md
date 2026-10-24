# CLI reference

## Global flags and device selection

```sh
garminctl [global flags] <command> [args]
garminctl -h
```

| Flag | Meaning |
| --- | --- |
| `-device ID` | Select the ID reported by `devices`. |
| `-root DIR` | Use a directory dump or mounted watch instead of opening USB. Defaults to `GARMINCTL_ROOT` when set. |
| `-json` | Return JSON where the command supports it. |

On Windows/WSL, `devices` lists portable devices Windows sees, including phones
and USB drives. Automatic selection opens the first device whose manufacturer
is Garmin or whose path contains USB vendor `091E`. On native Linux, the
libusb-enabled build lists Garmin USB devices and requires `-device ID` when
more than one is present. IDs are specific to each transport.

The portable CLI archive supports WPD on Windows/WSL and directory dumps;
direct Linux USB needs the CLI from the DEB/RPM package or a CGO-enabled source
build. See [installation](desktop.md).

## Commands

```sh
garminctl version
garminctl devices
garminctl info
garminctl ls GARMIN/Activity
garminctl cat -o Settings.fit GARMIN/Settings/Settings.fit
garminctl put local-file.bin GARMIN/NewFiles/local-file.bin
garminctl rm GARMIN/NewFiles/local-file.bin
garminctl mkdir GARMIN/Example
garminctl decode GARMIN/Sports/WalkingWalk.fit
garminctl settings
garminctl settings -all
garminctl settings set deviceSettings.backlightMode=manual userProfile.weight=79.5
garminctl apps
garminctl app-settings easy
garminctl app-schema easy bar1a
garminctl app-settings set easy seconds=off "bar1a=Body Battery %" cust_color1=FF7A2A
garminctl updates
garminctl updates install 006-DA094-00
garminctl software
garminctl activities -n 20
garminctl activity 2026-10-18-14-02-40.fit
garminctl health -days 14
garminctl health day 2026-10-22
garminctl workouts
garminctl workouts create plan.json -send
garminctl workouts create plan.json -local
garminctl music ls
garminctl music add song.mp3
garminctl music playlist Run track1 track2
garminctl app-rm "Simply Large"
garminctl app-hide Archery
garminctl app-show Archery
garminctl app-pending
garminctl app-restore ~/.cache/garminctl/OUT.BIN-…
garminctl autostart on
garminctl autostart status
garminctl autostart off
garminctl health extras
garminctl health history 365
garminctl health weight 80.5
garminctl hidden GARMIN/Apps/Media
garminctl desktop
garminctl serve
garminctl serve -addr 127.0.0.1:8791 -autosync=false
garminctl serve -autosync=false
garminctl mcp
garminctl store search solar face
garminctl store list -type widget -sort mostPopular
garminctl store info 7c993e2b-70b3-459a-bbe0-db48a89af5a8
garminctl backup ~/garmin-backup
garminctl cloud login
garminctl cloud mfa 123456
garminctl cloud status
garminctl cloud logout
garminctl cloud sync
garminctl cloud messages
garminctl cloud settings
garminctl cloud settings set timeFormat=time_twenty_four_hr
garminctl -root /path/to/dump info
```

`info` reports the model, unit ID, firmware, installed entry count, pending
files, and storage capacity/free space. `version` prints the version the binary
was built from.
`backup` copies the watch's storage to a local directory; music is left out by
default.

## Desktop, web server, and MCP

| Command | Behavior |
| --- | --- |
| `garminctl desktop` | Open the installed native app; from WSL, open the Windows installation. Install Setup or a Linux package first. It takes no arguments. |
| `garminctl serve` | Start the web UI, HTTP API, and MCP endpoint on `127.0.0.1:8787`; USB watching and automatic sync are enabled by default. |
| `garminctl serve -addr 127.0.0.1:8791 -autosync=false` | Choose a port and disable USB watching/automatic sync. Connect manually in the UI or through an MCP tool. |
| `garminctl mcp` | Run a separate stdio MCP server until the client disconnects. |
| `garminctl autostart on\|off\|status` | Manage the Windows sign-in shortcut for the background `serve` process. Works from Windows or WSL; does not launch the desktop window. |

The desktop window starts its own HTTP MCP endpoint at `/mcp` and shares one
watch connection with it. Connect assistants to the URL in **AI & MCP**; see
[MCP setup and installed-app verification](mcp.md). A separately launched
`garminctl mcp` has its own watch connection and session history.

The installed `garmin-desktop` executable accepts `-addr IP:PORT` and
`-root DIR`; these are application flags, not arguments to `garminctl desktop`.
`-root` also disables the automatic USB watcher. Full examples and Windows CLI
paths are in the [desktop guide](desktop.md).

Standalone CLI commands open their own USB connection. Close the desktop app
and stop background servers before using them against the same watch. The
`hidden` diagnostic currently requires WPD; `hidden -write-test PATH` writes
and deletes a temporary file and is a write test.

## Settings and updates

Settings addresses use `message.field`, with public FIT profile names such as
`deviceSettings`, `userProfile`, `hrZone`, and `zonesTarget`. Repeated messages
take an index (`hrZone[5].highBpm=180`); array fields take an element
(`deviceSettings.104[4]=8`); undocumented fields take a number. See
[formats](formats.md) for the settings-change file format and verified device
behavior.

`settings` shows documented fields; use `settings -all` for every field.
`managedByConnect` fields are controlled by Connect, which pushes its own copy
back on each Express or phone sync; change those in Garmin Connect too.

`updates` checks Garmin's software/data service and compares installed app
versions with the store. Software updates are downloaded, MD5-checked, and
written to Garmin's indicated path; the watch installs them when the USB
session ends. Connect IQ updates arrive through Connect's queue or as a
sideloaded `.PRG` through the web UI.

Connect IQ settings are written directly into the app's `.SET` file and take
effect immediately. Labels, dropdown options, and value ranges come from the
public settings page on apps.garmin.com and are cached under
`~/.cache/garminctl/appsettings/`; dropdown choices may be entered by label.

## Health, activities, workouts, and music

Activities are decoded from `GARMIN/Activity/*.fit` on the watch (sessions,
laps, and records) and cached in `~/.cache/garminctl/activities-<unit>.json`.
Daily health includes steps, resting heart rate, stress, body battery, sleep,
and calories; intraday data includes heart rate, stress, body battery, and
sleep stages. These come from Garmin Connect, which retains history the watch
itself discards after each sync. The web UI also maps activity GPS tracks; see
[web UI](web-ui.md).

Workouts use a JSON specification:

```json
{"name":"4x400","sport":"running","steps":[
  {"type":"warmup","duration":"10:00"},
  {"repeat":4,"steps":[{"type":"interval","distance":"400m","target":"pace 4:30-4:45"},
                       {"type":"recovery","duration":"1:30"}]},
  {"type":"cooldown","duration":"5:00","target":"hr zone 2"}]}
```

Step types are warmup, cooldown, interval, recovery, rest, and other. A step
has one end condition: `duration` (`10:00`, `90s`, `1h`), `distance` (`400m`,
`2km`, `1mi`), `calories`, or `lap: true`. Targets include `hr 140-160`,
`hr zone 3`, `pace 4:30-4:45` (min/km), `speed 10-12` (km/h),
`cadence 170-180`, and `power 200-250`. A workout can be saved in Connect and
queued to the watch, or written as a FIT file to `GARMIN/NewFiles`; both were
verified against Connect's generated file. Courses are not offered because
the vívoactive 5 has no course data type.

Streaming-app downloads (YouTube Music, Spotify, and others) are DRM-protected
and stored in the hidden app area; only their cache size is known and shown as
"Streaming apps". Own audio files (mp3, m4a, aac, wav, flac) are written to
`Music/<Artist>/<Album>/<Title>` from ID3 tags. Podcasts and audiobooks use
their corresponding folders; playlists are `.m3u` files in `Music/`.

## Connect IQ store and app registry

The public store API needs no login. `store` commands browse and search apps
filtered to this watch, with ratings, downloads, price, device-specific size,
permissions, description, release notes, and reviews; results indicate
installed apps and newer versions.

The store website no longer handles installs. Queue an app in the Connect IQ
phone app while the phone cannot reach the watch (Bluetooth off), then
`cloud sync` pulls the queued `.PRG` and writes it to the watch. The watch
hides its app folder from ordinary MTP listings; garminctl accesses it through
Garmin's vendor PTP operation and raw SendObject. A watch face installed this
way was registered by the watch on unplug on 2026-10-22. Sideloaded `.PRG`
files use the same path, including writes to hidden `GARMIN/Apps/Media`.

`app-hide` and `app-show` queue changes to the watch's app list for unplug;
`app-pending` shows queued changes and `app-restore` restores a saved registry.
`app-rm` removes a Connect IQ app, completed on unplug.
