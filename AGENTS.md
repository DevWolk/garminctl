# Agent notes

See CLAUDE.md for the working rules. Module map:

- `cmd/garminctl/main.go`: subcommands; every command opens the device via
  `openDevice` (MTP bridge or `-root` directory).
- `internal/device`: `Device` interface (List/Stat/Read/Write/Delete/Mkdir).
  `wpd/wpd.ps1` is the PowerShell+C# bridge, `wpd/client.go` its JSON-lines
  client. Protocol ops: devices, open, list, stat, read, write, delete,
  mkdir, quit; payloads are base64.
- `internal/fit`: `Decode`/`Encode` keep raw records; `Profile.Describe`
  names fields; `Builder` writes new files Garmin-style (local slot 0).
- `internal/garmin`: `Service` (cached reads), `SettingsView.Rows`,
  `Resolve`/`BuildChangeFile`/`ApplyChanges`, `AppSettings`/`SetAppSettings`,
  `ParseAppsConfig` (OUT.BIN protobuf), `ParseDeviceXML`,
  `CheckSoftwareUpdates`/`InstallSoftwareUpdate` (Garmin's OMT update
  service), `CheckAppUpdates`, `InstallPRG`, `SoftwareInventory`,
  `knowledge/device_settings.json` (notes on undocumented fields).
- `internal/gc`: Garmin Connect session (SSO sign-in with MFA, OAuth1 →
  OAuth2, token file, bearer helpers). `internal/garmin/cloud.go` builds the
  sync on it: `Status`, `Settings`/`SetSettings`, `Messages`/`Pull`,
  `Upload`, `Sync`.
- `internal/garmin/health.go` (activities from the watch's FIT files with a
  disk cache, daily/intraday health from Connect), `workouts.go`
  (`WorkoutSpec` → Connect JSON and workout FIT; create/send/delete),
  `music.go` (tracks, ID3 tags, playlists).
- `internal/ciqstore/store.go`: public store website API (`Browse`,
  `Detail`, `Reviews`, `DeviceTypeID`); `internal/garmin/store.go` merges it
  with the installed apps.
- `internal/ciqstore`: `Fetch`/`Load`/`Parse` of the apps.garmin.com
  settings page; `Setting.OptionValue` resolves labels to values; `AppInfo`
  reads the public store record (latest version).
- `internal/web`: `net/http` API + embedded `ui/` (vanilla JS).
- `internal/mcpserver`: go-sdk tools, all thin wrappers over `Service`.
