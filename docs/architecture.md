# How garminctl works

This page explains what garminctl is made of and why it is built this way. Read
it before you change the code, or when you want to know what the program does
to your watch. The [glossary](glossary.md) explains the user-facing terms;
[terms used on this page](#terms-used-on-this-page) covers the internal ones.

## Why it exists

garminctl is a personal project with two aims:

1. **Set up a Garmin watch through an AI assistant.** Watch settings, the
   settings of watch faces and apps, and workouts are changed with MCP tools
   instead of by hand.
2. **Practice building a larger program in Go.**

The command line, the web UI and the desktop app use the same code as the MCP
tools.

## Overview

One Go codebase builds two programs. `garminctl` is the CLI, the local server
and the MCP server. `garmin-desktop` is a native window that runs the same
server code. All of them are front ends to the same feature code.

```text
desktop window ──┐
browser ─────────┼── HTTP ──┐                         ┌─ WPD bridge (Windows, WSL)
AI assistant ────┘  + /mcp  ├─ features ─ watch owner ┼─ libusb MTP (Linux)
terminal ─────────── CLI ───┤                         └─ directory (-root DIR)
AI assistant ── stdio MCP ──┘
                               features ─ HTTPS ─ Garmin Connect, Connect IQ store,
                                                  Garmin's update service
```

- garminctl runs on the computer only; it puts no software of its own on the
  watch.
- Most changes are written as files that the watch applies by itself after you
  unplug it.
- Garmin Connect is optional. Without sign-in, everything that needs only the
  watch still works.
- The server listens on this computer only and has no accounts or passwords of
  its own.

## Front ends

| Front end | How it starts | Watch connection |
| --- | --- | --- |
| Desktop app | `garmin-desktop`, or `garminctl desktop` | Starts the local server, or reuses one that is already running for the same watch. |
| Web UI and HTTP API | `garminctl serve` | One connection, shared by every browser tab and HTTP MCP client. |
| MCP over HTTP | `/mcp` on the local server | The server's connection. |
| CLI | `garminctl COMMAND` | Its own connection, closed when the command ends. |
| MCP over stdio | `garminctl mcp` | Its own connection, for as long as the assistant keeps it running. |

The desktop app adds what a browser page cannot do: a Save dialog, a guard for
unsaved edits, and installing garminctl updates. See
[the desktop app](#the-desktop-app).

**Why several front ends over one implementation.** A feature such as
"change a watch setting" is written once, as an action. The CLI command, the
HTTP route and the MCP tool are thin adapters that call it. Not every feature
has all three: sideloading an app is in the HTTP API only, and `backup` is in
the CLI only.

**Why the UI is embedded.** The web UI is plain HTML, CSS and
JavaScript, compiled into the binary with its fonts and map library. Building
it needs no bundler or package manager, and the page loads without internet
access. Only map tiles are fetched online, when you open a map.

## Reaching the watch

Every feature talks to a small `Device` interface: list, stat, read, write,
delete, make a directory. Three transports implement it, and
[`devicehost`](../internal/ship/devicehost/open.go) picks one.

| Transport | Used when | How it works |
| --- | --- | --- |
| WPD bridge | Windows, and Linux inside WSL | garminctl starts Windows PowerShell with a small C# helper that calls the Windows Portable Devices API. They exchange one JSON message per line. |
| Native MTP | Linux, in a build with CGO | garminctl speaks MTP over libusb. Experimental: not verified on physical hardware. |
| Directory | `-root DIR` | `DIR` holds the watch's `GARMIN/` tree: a mounted watch, a copy made with `garminctl backup`, or test fixtures. |

**What the PowerShell bridge provides.** A watch in MTP mode has no
drive letter; Windows reaches it through Windows Portable Devices.
Windows PowerShell and the .NET compiler it uses are part of Windows, so the
bridge works without installing anything, and the Windows build needs no CGO.
One bridge also serves two builds: from WSL, the Linux binary starts it through
`powershell.exe` and uses the watch that Windows sees.

**What the directory transport provides.** Tests and development need
no watch, and a copy of a watch can be read with the same commands. Paths are
opened through `os.Root`, so a watch path cannot leave the directory. A
directory has no watch behind it: nothing applies pending files, and storage
size, hidden entries and raw MTP operations are unavailable.

The transports, the app folder that ordinary listings do not show, and the
write sequence are described in
[file formats and transport](formats.md#device-paths-and-transport).

## One owner of the watch per process

A watch connection carries one operation at a time. Inside one process, exactly
one object owns the watch: the
[`watchsession.Manager`](../internal/ship/watchsession/manager.go), built by the
[process runtime](../internal/app/process/runtime.go).

- **Leases.** A request borrows the connection for one bounded group of
  operations and returns it. Requests wait their turn; none of them holds the
  watch for the lifetime of the process.
- **Epochs.** Each connection has a number. When a connection ends, because
  the watch was unplugged or the connection was dropped, the work that started
  on it is cancelled. A later connection has a new number, and old work never
  continues on it.
- **Jobs.** Sync and activity decoding run for a long time. They take a lease
  for each group of files, so other requests can use the watch in between. A
  sync that is cancelled keeps what it had done: accepted uploads are recorded,
  and files not yet written stay in Garmin Connect's queue.
- **Admission gate.** One [gate](../internal/ship/lifecycle/gate.go) admits
  operations and jobs. Shutdown closes the gate, then waits for the admitted
  work to end. An application update needs the gate to itself and is refused
  while other work is active.

Separate garminctl processes do not coordinate with each other. Each has its
own connection, and all of them read the same saved sign-in.

**Why the HTTP MCP endpoint lives inside the server.** The assistant, the
browser and the desktop window then share one connection and one sign-in
instead of competing as separate processes. A stdio MCP server is a separate
process with its own connection.

**Why the desktop app reuses a running server.** Two processes would otherwise
open the same watch. The app reuses only a garminctl server that reads the same
watch or directory. For other data it starts its own server on a free port, and
it never takes over a port that another program holds.

## How changes reach the watch

Where the watch has an input mechanism, garminctl uses it instead of editing
the watch's own records.

| Change | What garminctl writes | Takes effect | Safety copy |
| --- | --- | --- | --- |
| Watch settings | A settings-change file in `GARMIN/NewFiles`. `Settings.fit` is never rewritten. | After you unplug the watch. | Not needed: the settings file itself is untouched. |
| Hide or show an app | `GARMIN/Apps/IN.BIN` | After you unplug the watch. | The current app registry is copied and read back first. |
| Connect IQ app settings | The app's `.SET` file | Immediately. | The current file is copied first. |
| Remove an app | Deletes the app's program, settings and data files. The registry is not edited. | The watch drops the app from its list after you unplug it. | None. |
| Workout written locally | A workout file in `GARMIN/NewFiles` | After you unplug the watch. | None; nothing is overwritten. |
| Watch software | The update file, at the path Garmin's service names | The watch installs it after you unplug. | None; the download's size and checksum are checked first, when Garmin supplies them. |
| Files and music | The file itself | Immediately. | None. |

If a safety copy cannot be written, the change is not made.

**Why the codecs keep what they do not understand.** The Connect IQ
settings file and the app registry are rewritten from their decoded form, so
the codecs must give back every byte they did not change. They keep unknown
fields, raw bytes and record order, and tests require that decoding and
encoding each fixture gives the same bytes. The registry writer also refuses to
write when re-encoding the unchanged registry would alter any byte. The formats
are in [formats.md](formats.md).

## Code layout

```text
cmd/garminctl          CLI entry point
cmd/garmin-desktop     native window entry point
internal/app           builds the process: one runtime, one account client, the adapters
internal/containers/   one directory per feature
    <feature>/         models and actions: the feature's behavior (its core)
    <feature>/ui/cli   command-line adapter
    <feature>/ui/http  HTTP adapter
    <feature>/ui/mcp   MCP adapter
internal/ship/         shared code: transports, codecs, Garmin clients, and the
                       web, MCP and desktop shells
tests/                 architecture tests, contract tests, native acceptance, UI tests
tools/                 icon generation and the Garmin Express release monitor
```

The features are `watch`, `settings`, `apps`, `health`, `workouts`, `music`,
`store`, `updates`, `connect` and `application`. `application` is the update of
garminctl itself; it has a CLI adapter and a `ui/native` adapter for the
desktop window instead of HTTP and MCP adapters.

### Terms used on this page

| Term | Meaning |
| --- | --- |
| Feature | One directory under `internal/containers`. |
| Core | A feature's own package: models and actions, without any interface code. |
| Action | One operation of a feature, such as "apply settings changes". Adapters call actions. |
| Adapter | A `ui/cli`, `ui/http`, `ui/mcp` or `ui/native` package that turns one interface's input and output into calls to actions. |
| Shell | The generic host of an interface: [`web`](../internal/ship/web) for HTTP, [`mcpserver`](../internal/ship/mcpserver) for MCP, [`desktop`](../internal/ship/desktop) for the window. |
| Process runtime | The object in [`internal/app/process`](../internal/app/process/runtime.go) that owns the watch owner and the long-running jobs of one process. |
| Watch owner | The `watchsession.Manager`. It hands out leases. |
| Account client | The Garmin Connect client, [`gc.Client`](../internal/ship/gc/client.go), with the saved sign-in. |
| Composition | The code in `internal/app` that creates these objects and hands the adapters to the shells. |

### Import rules

[`tests/architecture`](../tests/architecture) is part of `make check`. It
fails when an import breaks one of these rules:

| Rule | Reason |
| --- | --- |
| A core imports no interface code: not the shells, the adapters or the helpers `httpx`, `mcpx` and `clix`, and not `net/http`'s server types, the MCP SDK or Wails. | The behavior can be tested without an interface and reused by all of them. |
| Among features, an adapter imports only its own. | An interface of one feature does not depend on another feature. |
| Features import each other only through four declared edges: `connect`, `store` and `updates` use `apps`; `workouts` uses `connect`. | A new dependency between features is a reviewed decision. |
| The shells import no feature and no account client. They receive routes and tools from `internal/app`. | Features are plugged in at one place. |
| Only the process runtime builds the watch owner and the job coordinators. Only `internal/app` and its CLI package build the account client. | Inside a process there is one owner of the watch, of its jobs and of the sign-in. |
| The `application` feature has no HTTP or MCP adapter. | The server has no login, and an assistant acts without a person watching each step. Neither can replace the program or restore data; that starts only in the native window, after a confirmation. |
| A third-party library in a core or in shared code under `internal/ship` needs a listed allowance. | Dependencies stay few and reviewed. |

The declared edges are in `architectureFeatureEdges`, and the library
allowances in `architectureApprovedLibrary`, both in
[`architecture_test.go`](../tests/architecture/architecture_test.go).

### Where to add things

`music` is the smallest feature and a good one to read first.

| To add | Do this |
| --- | --- |
| An operation | Write an action in the feature's core. It receives a watch session from its caller and imports no interface code. |
| An HTTP route | Register it in the feature's `ui/http` package, in `RegisterRoutes`. Borrow the watch with `httpx.WithWatch`, and wrap a route that changes something in `httpx.Admit`. Add the pattern to the pinned list in [`http_routes_test.go`](../tests/contracts/http_routes_test.go). |
| An MCP tool | Register it in the feature's `ui/mcp` package, in `RegisterTools`, with `mcpx.WithWatch` and the annotations `mcpx.ReadOnly` or `mcpx.Writes`. Update the tool contract tests in [`tests/contracts`](../tests/contracts). |
| A CLI command | Add it to the feature's `ui/cli` package, then to a command table in [`internal/app/cli`](../internal/app/cli): `plainCommands` for commands without a watch, `sessionCommands` for one bounded operation, `jobCommands` for long jobs. Add the line to the usage text in `cli.go`. |
| A new feature | Create the directories above and list the adapters in [`internal/app/serve.go`](../internal/app/serve.go): the HTTP adapters in `web.Features`, the MCP adapters in `mcpTools`. |
| A UI tab | Add the `data-tab` button and its section to [`index.html`](../internal/ship/web/ui/index.html) and the behavior to `app.js`. The server serves the page for every declared tab name. |

[CONTRIBUTING.md](../CONTRIBUTING.md#documentation) lists the guides to update
with each of these.

## The local server

`garminctl serve` listens on `127.0.0.1:8787` by default and serves the web UI,
the [HTTP API](http-api.md) and the MCP endpoint.

The server has no login. Anything that can reach it can read and write
the watch, so a web page from elsewhere must not be able to call it through the
browser. [`LocalOnly`](../internal/ship/httpx/security.go) refuses:

- a `Host` header that is not `localhost` or an IP address, on the server's
  port. This stops DNS rebinding.
- an `Origin` other than the server's own.
- a browser request that another site started, other than a plain link into a
  UI page.

It also forbids other sites from framing the UI.

These checks are the only barrier, and they are about browsers. Other programs
on the same computer can call the server.

> [!WARNING]
> `garminctl serve -addr` accepts any address. On an address other than
> loopback, everyone who can reach the port can change the watch and use the
> signed-in Garmin Connect account. The desktop app accepts loopback addresses
> only.

### The desktop app

[`garmin-desktop`](../cmd/garmin-desktop/main.go) opens a native window with
Wails. The window loads the UI through a proxy that accepts requests from the
window's own origin only, and forwards them to the local server. On Windows it
uses WebView2; on Linux, GTK 3 with WebKitGTK 4.1.

The window talks to the `application` feature through native calls, not through
the server. That is how it can install updates although the server cannot.

## Online services

| Service | Used for | Sign-in |
| --- | --- | --- |
| Garmin Connect | Sync, health data, workouts, Connect-managed settings | Yes |
| Connect IQ store | Store browsing, app details, labels of app settings | No |
| Garmin's update service | Watch software and data updates. The request carries the watch's `GarminDevice.xml`. | No |
| GitHub Releases | Updates of garminctl itself, only when you check or download | No |
| OpenStreetMap | Map tiles, when an activity map is shown | No |
| `thegarth.s3.amazonaws.com` | The key pair that Garmin Connect sign-in needs, in builds that do not have it built in. Local builds are such builds; see [releases](releases.md#configure-the-garmin-connect-key). | No |

[`gc`](../internal/ship/gc/client.go) signs in the way the Garmin Connect
mobile app does, including the verification code. garminctl saves the resulting
tokens in a file that only your user account can read, and never saves the
password. MCP tools cannot sign in: the assistant is told to send you to the
app or the CLI, so a password never enters a conversation.

Garmin can change these services at any time.
[formats.md](formats.md#network-transfers-and-local-state) names the code that
defines each request.

## Updating garminctl itself

The **Application** tab of the desktop app can download and install a newer
release, or return to an older one. The design assumes that every step can
fail:

- **Signed packages.** Each release package has a signature. The public key is
  compiled into the binaries; no flag, environment variable or request can
  choose another key.
- **Download is separate from installation.** A download verifies the package
  and saves it. Installation starts only after you confirm; it verifies the
  package again and saves a backup of your garminctl data before anything is
  replaced.
- **A helper does the installation.** The app starts a helper process, stops
  its server and exits. The helper waits for that, verifies the package once
  more, and runs Windows Setup or the system package manager. On Linux the
  package manager step asks for authorization once. Then the helper starts the
  app again.
- **Native window only.** The HTTP API and MCP have no route or tool for it.
- **A handoff is not proof.** After the system installer takes over, the app
  reports what it can observe, and shows the installer's own result on the next
  start.

Policy (which release, which channel, what to keep) is in
[`containers/application`](../internal/containers/application); downloading,
verifying and installing are in
[`ship/updatetransport`](../internal/ship/updatetransport). The user's view is
in [updating garminctl](updating.md).

## Build and tests

- **Docker toolchain.** Make targets run in one container that holds Go, the
  GTK and WebKitGTK headers, libusb, NSIS and the packaging tools. Building and
  checking need Docker and `make` on the host, nothing else. Linters are pinned
  in `tools.mod`.
- **No watch needed.** Unit tests read fixtures through the directory
  transport.
- **Contract tests** ([`tests/contracts`](../tests/contracts)) run the
  production composition over fixtures. They pin the HTTP route list and the
  MCP tool definitions, and check that the HTTP and stdio MCP servers offer the
  same tools.
- **Architecture tests** ([`tests/architecture`](../tests/architecture)) check
  the [import rules](#import-rules).
- **Package acceptance.** CI installs the Windows, Ubuntu and Fedora packages,
  opens the native window, calls MCP against fixtures and uninstalls.

Physical USB behavior is not covered by automated tests.
[CONTRIBUTING.md](../CONTRIBUTING.md) lists the commands.

## Decisions at a glance

| Decision | Reason | Cost |
| --- | --- | --- |
| Two self-contained binaries from one codebase, with the UI embedded | Nothing else to install; the front ends share one implementation. | The UI is written without a framework or build step. |
| PowerShell and C# bridge on Windows | Uses what Windows already has, needs no CGO, and also serves a Linux binary in WSL. | A helper process per connection; file contents travel as base64 text. |
| Directory transport | Offline work and tests without hardware. | Fixtures do not prove what a real watch does. |
| One watch owner per process, with leases | A connection carries one operation at a time. | Separate processes do not share a connection; use HTTP MCP to share. |
| Change files in `NewFiles` | Uses the watch's own input folder instead of editing its settings record. | Changes appear only after you unplug the watch. |
| Codecs keep unknown data | Rewritten files keep every byte that was not edited. | Models hold raw records as well as decoded values. |
| Local server guarded by request checks, not a login | A single-user tool on one computer; the checks stop other websites. | Unsafe on a non-loopback address: there is no second barrier. |
| No application updates over HTTP or MCP | Replacing the program needs a person's confirmation in the native window. | The browser UI can only link to Releases. |
| Import rules checked by tests | Boundaries hold without relying on review. | New edges and libraries need a change in the tests. |
