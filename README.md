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

Manage a Garmin watch from your computer over USB, by hand or through an AI
assistant: change its settings, manage apps, workouts and music, browse its
files, and sync recordings with Garmin Connect.

## Why garminctl

garminctl lets an AI assistant set up your Garmin watch. You say what you want,
for example *"make the backlight manual, hide the Golf activity and add a
4 × 400 m interval workout"*, and the assistant makes the changes over USB.

Through [MCP](docs/mcp.md), an assistant can:

- change watch settings;
- change the settings of a watch face or another Connect IQ app;
- create a workout from a description and send it to the watch;
- hide, show and remove apps;
- install watch software updates and sync with Garmin Connect;
- read your activities and health data.

You can do the same by hand in the desktop app, in a browser or on the command
line. By hand you can also upload music, sideload apps, and upload or download
the watch's files.

### Compared with Garmin Express

Garmin Express is Garmin's own desktop program.

garminctl adds:

- control by an AI assistant or a script;
- changing watch settings on the computer, over USB;
- Linux, where USB support is still experimental.

garminctl lacks:

- **Garmin's support.** It is unofficial, and Garmin can change its services
  at any time.
- **Maps, marine charts, golf courses, product registration and Wi-Fi setup.**
- **Devices other than watches, and macOS.**
- **A list of supported watches.** Another model or software version can
  behave differently.
- **A publisher that Windows recognizes.** On Windows 11, Smart App Control
  can block garminctl's command-line program; see
  [Smart App Control](docs/desktop.md#smart-app-control).

garminctl does not replace or change Garmin Express, so you can keep both
installed and use Garmin Express for what only it does.

### Compared with other open-source tools

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
