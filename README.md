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
ends, and firmware updates restart it. Connect IQ app binaries cannot be
downloaded without a Garmin login, so the tool only reports which installed
apps have a newer store version; update those from the Connect IQ phone
app, or sideload a `.PRG` through the web UI.

Connect IQ app settings are written straight into the app's `.SET` file and
take effect immediately. Labels, dropdown options and value ranges come from
the public settings page on apps.garmin.com (cached under
`~/.cache/garminctl/appsettings/`), so dropdown choices can be given by
label.

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

Sections: Overview (device facts, pending NewFiles), Watch settings
(grouped, filterable, inline editing with dropdowns, one review step before
writing), Watch face & apps (edit any Connect IQ app's settings with the
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
selected app's store page. The JSON API behind it is in
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
the password).

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
