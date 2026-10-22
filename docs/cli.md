# CLI reference

## Commands

```sh
garminctl version
garminctl devices
garminctl info
garminctl ls GARMIN/Activity
garminctl cat -o Settings.fit GARMIN/Settings/Settings.fit
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
garminctl health extras
garminctl health history 365
garminctl health weight 80.5
garminctl hidden GARMIN/Apps/Media
garminctl serve -autosync=false
garminctl store search solar face
garminctl store list -type widget -sort mostPopular
garminctl store info 7c993e2b-70b3-459a-bbe0-db48a89af5a8
garminctl backup ~/garmin-backup
garminctl cloud login
garminctl cloud status
garminctl cloud sync
garminctl cloud settings
garminctl cloud settings set timeFormat=time_twenty_four_hr
garminctl -root /path/to/dump info
```

`devices` lists portable devices Windows sees. `info` reports the model, unit
ID, firmware, and pending files. `-root DIR` operates on a directory dump
instead of the watch. `version` prints the version the binary was built from.
`backup` copies the watch's storage to a local directory; music is left out by
default.

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
