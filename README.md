# garminctl

<!-- badges: DevWolk/garminctl#5 -->

## Summary

garminctl manages a vívoactive 5 over USB without Garmin Express. Use its CLI,
local web UI, or MCP server to manage watch files, settings, apps, health data,
and Garmin Connect sync.

Tested with firmware 17.05 from WSL2 on Windows 11. The watch uses the Windows
Portable Devices API built into Windows. The first call compiles the bridge
(about a second); no .NET runtime or extra drivers are needed.

## Demo

```sh
$ garminctl devices
$ garminctl info
$ garminctl settings
```

## Getting started

Download a pre-built Windows or Linux binary (amd64 or arm64) from the
[releases](https://github.com/DevWolk/garminctl/releases), unpack it, and put
`garminctl.exe` (Windows) or `garminctl` (Linux/WSL) on your `PATH`.
`checksums.txt` beside the archives contains SHA-256 sums.

With a Go toolchain, install the latest release or a specific version:

```sh
go install github.com/DevWolk/garminctl/cmd/garminctl@latest
go install github.com/DevWolk/garminctl/cmd/garminctl@v0.1.0
```

Start with these commands:

```sh
garminctl devices
garminctl info
garminctl ls GARMIN/Activity
```

Close Garmin Express before writing to the watch so both tools do not write at
the same time.

`GARMIN/Settings/Settings.fit` is never rewritten directly. Settings changes
are written as a type-41 file in `GARMIN/NewFiles`, which the watch applies
when the USB session ends; unplug or eject the watch afterwards.

## Features

- **CLI:** [commands and usage](docs/cli.md), including settings, files, health,
  workouts, music, and the Connect IQ store.
- **Watch formats:** [verified device and file behavior](docs/formats.md);
  [Easy+ theme](docs/easyplus-theme.md).
- **Web UI:** [tabs, filtering, and browser behavior](docs/web-ui.md).
- **MCP server:** [setup and tool list](docs/mcp.md).
- **Garmin Connect:** [sign-in, sync, and automatic USB sync](docs/cloud-sync.md).
- **Garmin Express parity:** [release watcher and latest assessment](docs/express-parity.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE).
