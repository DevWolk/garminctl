# garminctl

[![Release](https://img.shields.io/github/v/release/DevWolk/garminctl)](https://github.com/DevWolk/garminctl/releases/latest)
[![Tests](https://github.com/DevWolk/garminctl/actions/workflows/main.yml/badge.svg?branch=main)](https://github.com/DevWolk/garminctl/actions/workflows/main.yml)
[![License](https://img.shields.io/github/license/DevWolk/garminctl)](LICENSE)
[![Go version](https://img.shields.io/github/go-mod/go-version/DevWolk/garminctl)](go.mod)

Manage a Garmin watch over USB through a CLI, native desktop app, local web UI,
or MCP server. Browse files, edit settings, manage apps and workouts, and sync
recordings with Garmin Connect.

## Get started

Download a package from [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest)
and follow the [desktop installation guide](docs/desktop.md). It covers Windows,
WSL, Linux, and USB support limits. Native Linux USB remains experimental and
has not been verified on physical hardware.

For an existing installation, follow the [update instructions](docs/desktop.md#update-an-installed-package).

Launch **garminctl**, connect the watch by USB, and check **Overview**. Use
**Garmin Connect** to sign in for cloud data and sync, or **AI & MCP** to connect
an assistant.

For a terminal workflow, follow the [CLI installation and reference](docs/cli.md):

```sh
garminctl devices
garminctl info
garminctl settings
```

## Documentation

- [Desktop](docs/desktop.md): packages, launch options, lifecycle, and data locations.
- [CLI](docs/cli.md): commands, flags, settings, and workout specifications.
- [Web UI](docs/web-ui.md): browser access and available tabs.
- [MCP](docs/mcp.md): HTTP and stdio connections, tools, and session activity.
- [Garmin Connect](docs/cloud-sync.md): sign-in, manual sync, and automatic USB sync.
- [File formats](docs/formats.md): device files and write behavior.

## Contributing and license

See [CONTRIBUTING.md](CONTRIBUTING.md) for builds and checks.
garminctl uses the [MIT license](LICENSE).
