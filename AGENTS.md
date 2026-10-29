# Agent notes

See CLAUDE.md for the working rules. Module map:

- `cmd/garminctl`: `main.go` parses global flags (output goes to the `cli`
  writers so `main_test.go` can run commands over a dump) and dispatches through
  `plainCommand` (no watch: devices, desktop, serve, mcp, autostart, cloud login) and
  `sessionCommands` (settings and bounded watch commands) and `deviceCommands`
  (legacy features, including backup until its job workflow moves).
  Both watch paths use the same runtime/Manager lifecycle; extracted commands
  receive a scoped Session directly, and legacy commands receive Service.
  One file per command group: `files.go`, `settings.go`, `updates.go`,
  `cloud.go`, `health.go`, `workouts.go`, `music.go`, `store.go`,
  `serve.go`, `autostart.go`.
- `cmd/garmin-desktop`, `internal/desktop`: Wails v2 native window (build tag
  `desktop`), owned/reused loopback backend, private-origin proxy, native Save
  dialog and lifecycle/edit protection. `internal/devicehost` selects DirFS,
  WPD on Windows/WSL, or native Linux MTP.
- `internal/device/mtp`: native libusb transport on Linux with CGO; serial MTP
  transactions and filesystem operations, including Garmin's `0x9002` hidden
  listing. Non-CGO CLI builds retain WPD/DirFS support. Linux packages include
  the libusb-enabled CLI; the portable WSL CLI stays free of shared-library
  dependencies. Hardware verification of the native Linux path is pending.
- `internal/device`: `Device` interface (List/Stat/Read/Write/Delete/Mkdir).
  `DirFS` serves a directory through an `os.Root`, so watch paths cannot
  escape it. `wpd/wpd.ps1` is the PowerShell+C# bridge, `wpd/client.go` its
  JSON-lines client. Protocol ops: devices, open, list, stat, read, write,
  delete, mkdir, objectid, listid, mtpread, mtpwrite, mtpnodata, quit;
  payloads are base64. `wpd/raw.go` holds the raw PTP operations (hidden
  app folder, storage info).
- `internal/fit`: `Decode`/`Encode` keep raw records; `Profile.Describe`
  names fields; `Builder` writes new files Garmin-style (local slot 0).
- `internal/deviceinfo`: pure GarminDevice.xml models, parsing and software
  version formatting. `internal/garmin/devicexml.go` retains compatibility aliases
  while Session and feature containers are extracted.
- `internal/app` assembles runtime/account owners for entry points and supplies
  them to HTTP/MCP shells. `internal/ship/clix` owns CLI printing, errors and
  parsing; `cmd/garminctl/clix_compat.go` temporarily delegates existing commands.
- `internal/ship/httpx` owns local HTTP guards, request limits, JSON/error output
  and shared status mapping, watch lease responses, mutating admission and the
  route registrar contract. `internal/web/httpx_compat.go` temporarily delegates
  existing routes; Wails-specific private-origin validation stays in desktop.
- `internal/ship/mcpx` owns Streamable HTTP tracking/protocol close, generic
  watch lease handlers, tool registration and annotation helpers. Its narrow owner/lease contracts
  accept Session Manager directly and legacy leases without a garmin import.
  `internal/mcpserver` supplies Garmin bodies/runtime context and keeps delegates.
- `internal/ship/watchsession` owns cached/raw filesystem access, optional
  capabilities, scoped diagnostics, watch paths and metadata reads. Its Manager
  owns leases/epochs/probe/retirement/close. Legacy Lazy/Lease delegate to that
  owner and preserve one current Service facade with feature schema/profile
  caches. New adapters use Manager directly; no second owner is constructed.
- `internal/containers/settings` owns settings models, read/apply/schema Actions,
  private resolver/read tasks, FIT patch encoding and immutable settings Knowledge.
  Actions use watchsession directly and import no legacy/transport package.
  `ui/cli` owns CLI settings flags/output and uses the entry point's leased
  Session. `ui/http` registers the settings/schema/apply API through the shared
  Manager. `ui/mcp` registers the three typed settings tools through mcpx.
  App supplies route/tool registrars to HTTP and stdio shells with the same
  runtime owners. `garmin` retains model aliases/Service delegates.
- `internal/containers/watch` owns Summary and FIT Decode Actions over Session.
  Summary receives an AppCount capability from its caller; registry parsing
  stays with its existing owner. `ui/cli` owns discovery, ordinary file commands,
  summary/decode formatting and CLI-only scoped diagnostics. App supplies the
  temporary registry-count adapter without device ownership. Legacy Service
  delegates and aliases keep HTTP/MCP stable; backup still uses its legacy job.
- `internal/garmin`: `Service` (feature facade), `SettingsView.Rows`,
  `Resolve`/`BuildChangeFile`/`ApplyChanges`, `AppSettings`/`SetAppSettings`,
  `ParseAppsConfig` (OUT.BIN protobuf), `ParseDeviceXML`,
  `CheckSoftwareUpdates`/`InstallSoftwareUpdate` (Garmin's OMT update
  service), `CheckAppUpdates`, `InstallPRG`, `SoftwareInventory`,
  `containers/settings/knowledge/device_settings.json` owns settings notes;
  the old JSON remains a compatibility test fixture until facade removal.
- `internal/gc`: Garmin Connect session (SSO sign-in with MFA, OAuth1 →
  OAuth2, token file, bearer helpers). `internal/garmin/cloud.go` builds the
  sync on it: `Status`, `Settings`/`SetSettings`, `Messages`/`Pull`,
  `Upload`, `Sync`. `gc.CloudProfile`/`Client.SocialProfile` own the profile
  protocol and label mapping; caller-scoped profile caching and health/status
  error policy remain in Cloud while feature consumers are extracted.
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
  has no login) and request logging. The UI keeps the open tab and item in
  the URL path, so `handler` serves `index.html` for every first segment
  that `index.html` declares as a `data-tab`. `auto.go` is the USB watcher
  and the background sync job; `server_test.go` drives the API over a dump.
- `internal/mcpserver`: legacy go-sdk tools are thin wrappers over `Service`
  through `withWatch[In, Out]`; registration is grouped by area
  (`addWatchTools`, `addAppTools`, `addCloudTools`, ...). Extracted settings
  tools arrive through an injected registrar. Every tool has
  annotations (title, read-only/destructive, open-world) and a typed output;
  MCP requires object output schemas, so lists are wrapped (`filesResult`
  and friends) and recursive inputs are flattened (`workoutIn`: one level of
  repeat groups). `server_test.go` registers everything over an in-memory
  transport and calls the read tools against a dump, so a type the schema
  inferrer rejects fails the test instead of crashing `garminctl mcp`.
  `http.go` exposes the same tools over local Streamable HTTP with bounded
  session/tool-call metadata for the web UI's AI & MCP tab.

Quality gate: `make check` (module metadata, golangci-lint with govet,
race tests, govulncheck) must pass; the tools are pinned in
`tools.mod`. Make targets in `.make/` use `docker/docker-compose.yml` and a
host-UID toolchain container. `make package` builds native installers in `dist/`.
`make install` rebuilds the CLI and desktop binaries and rolls the CLI out on
this machine (`scripts/install.sh`: restarts whichever server runs, WSL or
the Windows autostart copy, keeping stopped servers stopped); `make update`
pulls the current branch first. The web and HTTP MCP servers share the `garmin.Lazy` facade over one
`watchsession.Manager` (one lazily opened watch); `internal/paths` names the cache directory; tests over a dump use
`internal/testdump`. `tools/expresswatch` monitors upstream release notes
(state in `docs/express-release.json`, workflow `express-watch.yml`). gocyclo is capped at 15 and nothing is
excused from it; a `//nolint` names its linter and states the reason.
