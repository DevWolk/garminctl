# Agent notes

See CLAUDE.md for the working rules. Module map:

- `cmd/garminctl`: `main.go` parses global flags (output goes to the `cli`
  writers so `main_test.go` can run commands over a dump) and dispatches through
  `plainCommand` (no watch: devices, serve, mcp, autostart, cloud login) and
  the `deviceCommands` table (everything else opens the device via
  `openDevice`, MTP bridge or `-root` directory, and closes it afterwards).
  One file per command group: `files.go`, `settings.go`, `updates.go`,
  `cloud.go`, `health.go`, `workouts.go`, `music.go`, `store.go`,
  `serve.go`, `autostart.go`.
- `internal/device`: `Device` interface (List/Stat/Read/Write/Delete/Mkdir).
  `DirFS` serves a directory through an `os.Root`, so watch paths cannot
  escape it. `wpd/wpd.ps1` is the PowerShell+C# bridge, `wpd/client.go` its
  JSON-lines client. Protocol ops: devices, open, list, stat, read, write,
  delete, mkdir, objectid, listid, mtpread, mtpwrite, mtpnodata, quit;
  payloads are base64. `wpd/raw.go` holds the raw PTP operations (hidden
  app folder, storage info).
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
- `internal/web`: `net/http` API + embedded `ui/` (vanilla JS). `handler`
  wraps the mux in `localOnly` (Host/Origin/Sec-Fetch-Site checks; the API
  has no login) and request logging. `auto.go` is the USB watcher and the
  background sync job; `server_test.go` drives the API over a dump.
- `internal/mcpserver`: go-sdk tools, all thin wrappers over `Service`
  through `withWatch[In, Out]`; registration is grouped by area
  (`addWatchTools`, `addAppTools`, `addCloudTools`, ...). Every tool has
  annotations (title, read-only/destructive, open-world) and a typed output;
  MCP requires object output schemas, so lists are wrapped (`filesResult`
  and friends) and recursive inputs are flattened (`workoutIn`: one level of
  repeat groups). `server_test.go` registers everything over an in-memory
  transport and calls the read tools against a dump, so a type the schema
  inferrer rejects fails the test instead of crashing `garminctl mcp`.

Quality gate: `make check` (golangci-lint with `.golangci.yml`, govet
included, race tests, govulncheck) must pass; the tools are pinned in
`tools.mod`. The web and MCP servers share `garmin.Lazy` (one lazily opened
watch); `internal/paths` names the cache directory; tests over a dump use
`internal/testdump`. `tools/expresswatch` is the daily Garmin Express
release check (state in `docs/express-release.json`, workflow
`express-watch.yml`); `docs/express-parity.md` is where the Copilot task
records whether garminctl had to follow a release. gocyclo is capped at 15 and nothing is
excused from it; a `//nolint` names its linter and states the reason.
