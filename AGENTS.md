# Agent notes

See CLAUDE.md for the working rules. Module map:

- `cmd/garminctl`: `main.go` holds the build version (`-X main.version`) and
  dispatches the private `_application-stage` worker before ordinary commands.
  `_application-verify` uses the same bounded protocol with a local-only asset
  source; neither command accepts extra arguments or a request-supplied key.
  `main.updatePublicKey` is compiled-only base64 RSA-3072 SPKI DER; empty
  builds refuse staging. Make/GoReleaser embed `build/update-public-key.b64`,
  the existing certificate's public key. Release signing reuses the existing
  Windows PFX secrets; PR package signing stays disabled. The worker runs only in a marked private cache
  operation, without watch ownership, a native window or installer execution.
  Normal dispatch
  calls `cli.Run` in `internal/app/cli`, which parses global flags (output goes
  to the `cli` writers so tests can run commands over a dump) and dispatches through
  `plainCommand` (no watch: devices, desktop, serve, mcp, autostart, cloud login) and
  `sessionCommands` (settings, apps, store, updates/software, cloud, bounded music and watch commands) and `jobCommands`
  (`cloud sync` and the activities and backup jobs).
  `sessionJob` routes music add/rm through the same job/epoch guard with direct
  per-file Session leases. Both watch paths use the same runtime/Manager lifecycle;
  bounded commands receive a scoped Session, while jobs start after releasing
  the initial lease and use their existing coordinator or per-file leases.
  One file per command group: `files.go`, `settings.go`, `updates.go`,
  `cloud.go`, `health.go`, `workouts.go`, `music.go`, `store.go`,
  `serve.go`, `autostart.go`; extracted app CLI wrappers live in `apps_cli.go`.
- `cmd/garmin-desktop`, `internal/desktop`: pinned Wails v3 native window (build tag
  `desktop`), owned/reused loopback backend, private-origin proxy, native Save
  dialog and lifecycle/edit protection. A private windowHost isolates bridge calls;
  native initialization precedes backend startup and shutdown joins owned work.
  The outer Assets middleware validates private origins before Wails dispatch;
  `/wails/runtime` accepts POST, while runtime modules remain available over GET.
  Native HTML receives a fresh proxy nonce through httpx's rendering contract;
  web marks only its embedded template and desktop appends its own bridge node.
  Strict CSP blocks untrusted inline handlers and parser-inserted srcdoc scripts.
  The proxy preserves native body streams with no advertised length before
  forwarding them to the backend's bounded request readers.
  Linux uses the gtk3 compatibility tag with WebKitGTK 4.1; native package/UI
  acceptance is tracked separately from headless tests. `internal/devicehost` selects DirFS,
  WPD on Windows/WSL, or native Linux MTP.
- `internal/containers/application` owns SemVer/channel selection, saved history,
  independent desktop/CLI/backend inventory and DownloadReleaseAction. Stable is
  the default and drafts are excluded; the core imports no Wails or watch owner.
  Download re-observes confirmed installation and matching desktop/CLI builds,
  then rechecks exact-tag/channel metadata before package IO. App maps its ports
  to the existing resolver, cached CLI stager and protected PackageCache.
  `ui/native` binds Info/History/Check and StartDownload/DownloadStatus/
  CancelDownload. App assembles these lazily from compiled version/key and trusted
  backend identity. Registration does no IO and creates no watch runtime.
  One download holds the existing window Scope until worker/cache cleanup ends.
  Cancel uses an operation ID; canceling stays active until join, and stale
  requests/callbacks cannot affect the next operation. Window close cancels and
  joins preparation before closing an owned backend; reused backend remains.
  The Application tab needs no watch. Opening it or changing channel never
  refreshes the release feed or starts a download. Notes are text, progress counts
  bytes and reload only resumes status polling. Browser access shows backend
  metadata and Releases. Missing compiled key disables preparation while keeping
  metadata reads available. No public HTTP/MCP installer or install/rollback UI.
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
  version formatting. watchsession and the connect, health, music and workouts
  containers import it directly.
- `internal/app` assembles runtime/account owners for entry points and supplies
  them to HTTP/MCP shells. `internal/app/process` owns one process's watch
  Manager, sync and activity jobs, Connect message claims and app schema cache;
  shells receive it through their own Runtime interfaces. One MCP registrar
  list serves HTTP and stdio; protocol contracts use the production composition
  with fixture transports.
  `internal/ship/clix` owns CLI printing, errors and
  parsing; `internal/app/cli/clix_compat.go` temporarily delegates existing commands.
- `internal/ship/httpx` owns local HTTP guards, request limits, JSON/error output
  and shared status mapping, watch lease responses, mutating admission and the
  route registrar contract. `internal/web/httpx_compat.go` temporarily delegates
  existing routes; Wails-specific private-origin validation stays in desktop.
- `internal/ship/mcpx` owns Streamable HTTP tracking/protocol close, generic
  watch lease handlers, tool registration and annotation helpers. Its narrow
  owner/lease contracts accept the Session Manager directly.
  `internal/mcpserver` supplies protocol instructions and registered-tool
  construction; app binds feature bodies to the shared runtime.
- `internal/ship/watchsession` owns cached/raw filesystem access, optional
  capabilities, scoped diagnostics, watch paths and metadata reads. Its Manager
  owns leases/epochs/probe/retirement/close. The process runtime builds the one
  Manager; adapters use it directly and construct no second owner.
- `internal/containers/settings` owns settings models, read/apply/schema Actions,
  private resolver/read tasks, FIT patch encoding (`BuildChangeFile`) and
  immutable settings Knowledge (`knowledge/device_settings.json`). Actions use
  watchsession directly and import no transport package.
  `ui/cli` owns CLI settings flags/output and uses the entry point's leased
  Session. `ui/http` registers the settings/schema/apply API through the shared
  Manager. `ui/mcp` registers the three typed settings tools through mcpx.
  App supplies route/tool registrars to HTTP and stdio shells with the same
  runtime owners.
- `internal/containers/apps` owns App/AppChange, registry (OUT.BIN protobuf)
  parsing/rewriting, verified private registry backups and
  ReadRegistry/Visibility/Pending/Restore/Cancel Actions over the caller's
  Session. SET/item/hint DTOs and immutable CIQ Knowledge also belong to apps.
  Find/SET/schema Actions and SchemaCache logic are apps-owned. SessionSchemas
  retains one cache, for the newest connection that asked; the current
  Session's first lookup replaces it, other lookups never do, and Close drops
  it. PRG staging/named delivery/list/removal Actions are apps-owned and use
  the same Session/cache. `ui/cli` owns eight app commands and calls Actions
  over scoped Sessions. The process runtime's
  AppSchemaCacheFor supplies the current connection's cache under the caller's
  lease without acquiring/opening again. `ui/http` registers nine app
  routes and `ui/mcp` registers six typed app tools with that same Manager/cache
  provider. App supplies both registrars and watch's count from apps.
- `internal/containers/watch` owns Summary/FIT Decode Actions over Session and
  BackupAction over a caller-supplied file source.
  Summary receives an AppCount capability from its caller; registry parsing
  stays with its existing owner. `ui/cli` owns discovery, ordinary file commands,
  summary/decode formatting and CLI-only scoped diagnostics. App supplies the
  registry-count adapter without device ownership. Backup CLI flags/output live in
  `ui/cli`; the existing CLI job wrapper owns admission/epoch binding, with each
  source call leasing Session separately and host writes outside that lease.
  `ui/http` owns file/list/decode/download handlers. App injects their registrar
  and a Session-based Summary reader into web's connection-state aggregation.
  Watcher/auto/disconnect remain shell-owned; the shell's refresh route calls
  app's Refresh, which drops the Session's file cache and its app schema cache.
  `ui/mcp` owns the four watch tools, including object result schemas and the
  400-message decode cap; app supplies the same registrar for HTTP and stdio.
- `internal/containers/workouts` owns WorkoutSpec/StepSpec/CloudWorkout, pure
  FIT/Connect encoding and local/list/create/send/delete Actions. Account and
  workout-only Delivery ports are supplied by callers. Connect owns PulledFile;
  workouts retains an alias. ui/cli owns list/create/send output over
  the entry point's scoped Session; ui/http owns five routes and ui/mcp three
  typed tools. App supplies a Connect-backed filtered queue port without a
  nested lease. HTTP note and MCP sendError policies remain adapter-specific.
- `internal/containers/health` owns activity models/FIT decoding/cache, the shared
  ActivityCoordinator over Manager, and daily/history/day/extras/weight Actions.
  AccountAccess borrows protocol, caller-owned Connect identity/metadata and cache
  path. The process runtime builds the one ActivityCoordinator; no second
  decoder or watch owner is created. ui/cli owns health/activity output and the
  daily/history cutoff; activities keeps its initial connect/release dispatch.
  ui/http owns six routes and ui/mcp seven typed tools over the shared decoder.
- `internal/containers/connect` owns Cloud status/profile cache/Forget policy,
  cloud settings, message queue routing/delivery/ack, uploads/ledger and the
  existing SyncCoordinator. Bounded Actions borrow Session; job Actions use the
  same Manager and epoch with per-group leases. Named PRG delivery uses apps;
  app supplies canonical schema invalidation and health/workout consumer ports.
  ui/cli owns account/raw/status/settings/messages/sync formatting. ui/http owns
  nine routes and ui/mcp four typed tools; app injects the same account and
  coordinator into HTTP/MCP/auto. HTTP keeps 202/409; compatible MCP waiters join
  the runtime job.
- `internal/gc`: Garmin Connect protocol session (SSO/MFA, OAuth1 to OAuth2,
  protected token state, bearer helpers). CloudProfile/SocialProfile own the
  profile protocol and label mapping. Caller-scoped identity cache/error policy
  and queue/upload workflows belong to containers/connect.
- `internal/containers/music` owns library/track/playlist/provider DTOs, four
  stateless Actions, ID3 codecs and media path/name helpers over Session.
  It reads provider metadata from device XML, with no apps registry dependency.
  Music and health each keep an identical private title-case helper; music
  does not import health. ui/cli owns list/
  playlist formatting and host reads with supplied per-file lease callbacks.
  CLI routing retains initial connect, job admission and epoch binding; add
  progress stays inside its lease and rm progress follows release. ui/http owns
  four guarded routes and ui/mcp two typed tools, supplied by app to both shells.
- `internal/containers/updates` owns watch software manifest/staging/inventory
  and app-version comparison Actions, DTOs and private HTTP helpers. Config
  supplies the endpoint (`DefaultURL`, Garmin's OMT update service) and client;
  a scoped registry reader supplies apps. Successful staging reports its
  invalidation generation to the shared app schema cache. ui/cli owns
  check/install/inventory output and partial-error exit rules; ui/http
  registers four guarded routes and ui/mcp two typed tools. App injects
  `updates.HTTPClient` (tests may replace it), the Session-scoped registry and
  the canonical cache Reset callback.
- `internal/containers/store` owns catalog View/App/Detail, Browse/Detail Actions,
  installed-version decoration and the process device-type cache. A scoped
  InstalledApps callback supplies the apps registry; nil/error leaves records
  undecorated. Three ui adapters
  use the same Actions; app supplies Registry over the existing leased Session.
  HTTP borrows only a current watch, while CLI/MCP require its XML.
- `internal/ship/hostinfo` owns regular executable discovery, a bounded CLI
  version child and read-only dpkg/rpm/Windows InstallDir queries. It imports
  no Application policy or Wails runtime; native inventory uses app source adapters.
- `internal/ship/updatetransport` owns bounded, cancellable GitHub release
  metadata reads for the compiled repository. It retains raw channel flags;
  Application owns selection policy. Requests use no cookies or redirects,
  and metadata pagination never follows response URLs. GitHub remains a
  metadata-only client. The package imports no feature or native runtime.
  Its pure MatchAssets function selects exactly one package and .sig sibling
  for the publishing filenames and supported OS/arch/install kind. It returns
  indexes into the caller's asset snapshot and checks declared sizes; it does
  not fetch, authenticate or install bytes. Release/channel policy stays in
  Application. Resolve checks the selected tag, lists assets by its numeric
  release ID and returns copies of the matched package/signature references.
  It bounds all pages, rejects duplicate IDs and requires canonical repository
  asset URLs; inline assets and response URLs never choose an endpoint.
  Resolved metadata is not authenticated or installable. Provider adapts one
  owned selection to the pinned headless Wails pkg/updater, fetching the exact
  raw sibling signature and streaming only the owned package URL. It requires
  exact actual size, bounds reads/time and strips credentials on HTTPS-only
  redirects. Composition supplies the compiled RSA-3072 key to Provider.
  Download succeeds only after exact-size streaming and mandatory SHA-256/RSA
  verification through Go crypto/rsa. Wails supplies staging; its digest-only
  configuration is never sufficient authentication. Worker IPC and retained
  catalogs use version 2 and reject legacy Ed25519 formats. HistoryCache stores
  versioned display metadata for the compiled source in application-history,
  bounded to 20 MiB/1000 records with private atomic file replacement. Missing,
  corrupt, foreign-source or unsupported snapshots fail explicitly. Package
  retention/recovery storage is separate; there is no installer or new watch
  owner. Stager runs a private cached CLI with bounded
  versioned IPC, operation/build/key identity and joined cancellation/cleanup.
  Worker retains the original signed stream and .sig before Wails archive
  extraction, then parent verifies the retained digest after child exit.
  StagedPackage owns its operation until Close. App supplies trusted CLI/version/
  compiled-key/cache composition to native preparation, retaining verified bytes
  before closing temporary operations. Installation authority remains separate.
  PackageCache retains package/signature pairs under application-packages,
  apart from application-staging operations. Put verifies copied bytes before
  whole-directory publication. List returns unverified metadata; Open uses a
  fresh cached CLI and the current compiled key to verify offline, returning
  an independently owned StagedPackage copy. The local asset transport never
  falls back to HTTP. Manifests have bounded reads and safe hex IDs; normal
  errors clean only owned pending/operation directories. No automatic eviction,
  process-crash sweep or installation is performed by this service.
- `internal/ciqstore/store.go`: public store website API (`Browse`,
  `Detail`, `Reviews`, `DeviceTypeID`); the store container merges it
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
- `internal/mcpserver`: MCP instructions and stdio/HTTP shells receive all
  feature tool registrars from app. Stdio borrows the narrow Context/Close
  runtime contract; HTTP borrows its parent context. The shell imports no
  feature package or account client. Lease/error handling belongs to mcpx and
  the feature adapters. Every tool has
  annotations (title, read-only/destructive, open-world) and a typed output;
  MCP requires object output schemas, so the feature `ui/mcp` packages wrap
  lists (`filesResult` and friends in containers/watch) and flatten recursive
  inputs (`workoutIn` in containers/workouts: one level of repeat groups).
  Contract tests in `tests/contracts` run the stdio composition through
  `app.ServeMCPTransport` over an in-memory transport and the HTTP composition
  through `app.ServeListener`. They retain
  result/schema/annotation digests and compare all 33 tool definitions between
  transports. Guard classification tests live beside mcpx; the shell checks
  protocol shutdown and joined runtime cleanup errors. `internal/ship/mcpx/http.go`
  exposes the same tools over local Streamable HTTP with bounded
  session/tool-call metadata for the web UI's AI & MCP tab.

Quality gate: `make check` (module metadata, golangci-lint with govet,
race tests, headless UI tests, govulncheck) must pass; the tools are pinned in
`tools.mod`. Make targets in `.make/` use `docker/docker-compose.yml` and a
host-UID toolchain container. `make package` builds native installers in `dist/`.
`make install` rebuilds the CLI and desktop binaries and rolls the CLI out on
this machine (`scripts/install.sh`: restarts whichever server runs, WSL or
the Windows autostart copy, keeping stopped servers stopped); `make update`
pulls the current branch first. The web and HTTP MCP servers share one
`process.Runtime` and account client from `app.ServeListener`: one
`watchsession.Manager` (one lazily opened watch), the sync and activity jobs
and the app schema cache. `internal/paths` names the cache directory; tests
over a dump use `internal/testdump`, which owns the shared XML and
app-registry fixtures. Unit tests stay beside their package; application
contract suites live in `tests/contracts` (HTTP and MCP) and
`tests/contracts/cli`, and the architecture gate in `tests/architecture`.
`tools/expresswatch` monitors upstream release notes
(state in `docs/express-release.json`, workflow `express-watch.yml`). gocyclo is capped at 15 and nothing is
excused from it; a `//nolint` names its linter and states the reason.
