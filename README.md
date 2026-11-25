# garminctl

[![Release](https://img.shields.io/github/v/release/DevWolk/garminctl)](https://github.com/DevWolk/garminctl/releases/latest)
[![Tests](https://github.com/DevWolk/garminctl/actions/workflows/main.yml/badge.svg?branch=main)](https://github.com/DevWolk/garminctl/actions/workflows/main.yml)
[![License](https://img.shields.io/github/license/DevWolk/garminctl)](LICENSE)
[![Go version](https://img.shields.io/github/go-mod/go-version/DevWolk/garminctl)](go.mod)

> [!IMPORTANT]
> garminctl is an independent open-source project and is not affiliated with,
> endorsed by or supported by Garmin Ltd. Garmin, Garmin Connect and Connect IQ
> are trademarks of Garmin Ltd. or its subsidiaries. garminctl talks to the same
> Garmin services as Garmin's own apps, and Garmin may change them at any time.
> Use at your own risk.

Manage a Garmin watch from your computer over USB: browse its files, change
its settings, manage apps, workouts and music, and sync recordings with Garmin
Connect.

## What you can do

- Change watch settings and Connect IQ app settings.
- Hide, show, remove and sideload apps; browse the Connect IQ store.
- See activities with maps and charts, and daily health from Garmin Connect.
- Create workouts and send them to the watch.
- Upload music and build playlists.
- Check for and install watch software updates.
- Sync with Garmin Connect, by hand or when the watch is plugged in.
- Let an AI assistant read the watch and make most of these changes through
  MCP.

## Why garminctl

Garmin's own desktop program is Garmin Express. garminctl is an independent
alternative with a different focus: the watch itself, over USB, from a window,
a terminal or an AI assistant.

What garminctl offers:

- **Watch settings without Garmin Connect.** garminctl reads the settings from
  the watch and writes your changes over USB, with a review before they are
  saved. It needs no account and no internet access. With Garmin's tools you
  change watch settings in the Garmin Connect app or website, or on the watch,
  and a sync applies them.
- **Automation.** Everything can be run from a command line or by an AI
  assistant, so a change can be scripted or asked for in plain words. Garmin's
  pages describe no command line or API for Garmin Express.
- **Linux.** Garmin Express runs on Windows and macOS only. In garminctl, USB
  on native Linux is still experimental.
- **No Garmin account for the watch.** Settings, apps, files, music and
  workouts written straight to the watch work without signing in. An account
  is needed for sync, health data and Garmin Connect workouts.
- **Nothing in the background**, unless you turn on autostart. Garmin Express
  is designed to run as a background service, which can be turned off.
- **The watch's files.** Browse, copy and delete them, and open Garmin's data
  files in readable form.
- **Open source**, under the MIT license.

What you give up:

- **Garmin's support.** garminctl is unofficial. Garmin can change its
  services at any time, and a feature can stop working until garminctl is
  updated.
- **Maps, marine charts, golf courses, product registration and Wi-Fi setup.**
  Garmin Express has them; garminctl does not.
- **Devices other than watches, and macOS.**
- **A list of supported watches.** garminctl has none. It works with the files
  a watch shows over USB, and another model or software version can behave
  differently.
- **A publisher that Windows recognizes.** On Windows 11, Smart App Control
  can block garminctl's command-line program; see
  [Smart App Control](docs/desktop.md#smart-app-control).

garminctl does not replace or change Garmin Express, so you can keep both
installed and use Garmin Express for what only it does.

Among open-source tools, the closest is
[openExpress](https://github.com/TomaszBojanowski/openExpress), a Linux app for
sync, updates, maps, apps and music over USB. As far as we found, garminctl is
the only open-source tool that changes watch settings over USB, and the only
one that changes the settings of watch apps (Connect IQ) on a computer. It runs
on Windows too.

[garminctl and the alternatives](docs/alternatives.md) compares them feature by
feature, with links to the sources.

## Four ways to use it

| Interface | Start it | Guide |
| --- | --- | --- |
| Desktop app | Open **garminctl** | [Desktop app](docs/desktop.md) |
| Web UI in a browser | `garminctl serve`, then open `http://127.0.0.1:8787` | [Web UI](docs/web-ui.md) |
| Command line | `garminctl COMMAND` | [CLI](docs/cli.md) |
| AI assistant (MCP) | The desktop app or `garminctl serve` at `/mcp`, or `garminctl mcp` | [MCP](docs/mcp.md) |

The browser, the desktop window and the assistant all talk to one local server
inside garminctl, which also has an [HTTP API](docs/http-api.md). By default it
listens on `127.0.0.1`, an address that only your computer can reach.

## Get started

Before you install:

- On Windows 11, read [Smart App Control](docs/desktop.md#smart-app-control)
  first.
- USB on native Linux is experimental and has not been verified on physical
  hardware; see [USB access](docs/desktop.md#usb-access).
- To update an existing installation, see
  [updating garminctl](docs/updating.md).

Then:

1. Download the package for your system from
   [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest)
   and install it. The [desktop guide](docs/desktop.md#install) names the file
   for Windows, WSL, Ubuntu and Fedora.
2. Open **garminctl** and connect the watch by USB.
3. Check **Overview**. It shows the watch, its storage and pending changes.
4. Optional: open **Garmin Connect** to sign in for health data and sync, or
   **AI & MCP** to connect an assistant.

Or in a terminal, with the [CLI](docs/cli.md):

```sh
garminctl devices
garminctl info
garminctl settings
```

After Windows Setup the `garminctl` command is not on `PATH`; see
[run the CLI on Windows](docs/desktop.md#run-the-cli-on-windows).

## Good to know

- Most changes take effect **after you unplug the watch**: watch settings,
  hidden apps, new workouts, software updates. See
  [when changes take effect](docs/cli.md#when-changes-take-effect).
- Before it hides or shows an app, or changes an app's settings, garminctl
  saves a copy of the watch file.
- Garmin Connect keeps its own copy of some settings and sends it again on a
  later sync. Change those fields in Connect too; see
  [settings that Connect manages](docs/cloud-sync.md#settings-that-connect-manages).
- garminctl goes online only for the features that need it: Garmin Connect,
  the Connect IQ store, Garmin's update service, map tiles, and GitHub when you
  check for a garminctl update. The list is in
  [online services](docs/architecture.md#online-services).

## Build from source

You need Docker with Compose v2 and `make`. The toolchain runs in a container.

```sh
make image-build   # build the toolchain image, once
make all           # CLI and desktop binaries in bin/
make check         # the quality gate: linters, tests, vulnerability checks
make install       # rebuild, and restart ./bin/garminctl serve if it is running
make help          # every target
```

Run a build from the checkout with `bin/garminctl serve`, or offline with
`bin/garminctl -root DIR serve`, where `DIR` contains a copy of `GARMIN/`.
[CONTRIBUTING.md](CONTRIBUTING.md) has the details.

## Documentation

Using garminctl:

- [Desktop app](docs/desktop.md): install, launch options, data locations, uninstall.
- [Web UI](docs/web-ui.md): the tabs and how to apply changes.
- [CLI](docs/cli.md): every command, with the workout format.
- [MCP](docs/mcp.md): connect an AI assistant; the tool list.
- [Garmin Connect](docs/cloud-sync.md): sign-in, manual and automatic sync.
- [Updating garminctl](docs/updating.md): updates, going back to an older version, recovery.
- [Troubleshooting](docs/troubleshooting.md): messages and what to do about them.
- [Glossary](docs/glossary.md): the terms used here.
- [Alternatives](docs/alternatives.md): Garmin Express and other open-source tools, compared.

Understanding and changing it:

- [How garminctl works](docs/architecture.md): the design and the reasons behind it.
- [HTTP API](docs/http-api.md): the local server's routes.
- [File formats](docs/formats.md): watch files, codecs and network requests.
- [Releases](docs/releases.md): building packages, signing and publishing.
- [CONTRIBUTING.md](CONTRIBUTING.md): setup, checks and change requirements.

In a release archive this file comes without the `docs` folder; the guides are
at <https://github.com/DevWolk/garminctl>.

## License

garminctl uses the [MIT license](LICENSE).
