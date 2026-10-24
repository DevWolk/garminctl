# garminctl

<!-- badges: DevWolk/garminctl#5 -->

## Summary

garminctl manages a vívoactive 5 over USB without Garmin Express. Use its CLI,
native desktop app, local web UI, or MCP server to manage watch files, settings, apps, health data,
and Garmin Connect sync.

Live USB access has been tested with firmware 17.05 from WSL2 on Windows 11,
using the Windows Portable Devices API. The first call compiles the bridge
(about a second); no .NET runtime or extra drivers are needed. Native Linux
USB uses libusb/MTP and still needs verification on physical hardware.

## Demo

```sh
$ garminctl devices
$ garminctl info
$ garminctl settings
```

## Getting started

### Desktop application

1. Open the [latest release](https://github.com/DevWolk/garminctl/releases/latest)
   and download the installer for your system:

   | System | Download | Install and launch |
   | --- | --- | --- |
   | Windows 10/11 x64 | `*_windows_amd64_setup.exe` | Open Setup, finish installation, then open **garminctl** from Start or the desktop shortcut. |
   | WSL | The Windows Setup above | Install and launch on Windows; the application accesses the watch through Windows. |
   | Ubuntu 24.04+ x64 | `*_linux_amd64.deb` | Open the package installer, or run `sudo apt install ./garminctl_*.deb`, then launch **garminctl** from the application menu. |
   | Fedora x64 | `*_linux_amd64.rpm` | Open the package installer, or run `sudo dnf install ./garminctl_*.rpm`, then launch **garminctl** from the application menu. |

2. Connect the watch by USB. The application opens its own window and starts
   its local server automatically. No Go, Docker, browser PWA, or separately
   started server is required. On Windows, Setup installs WebView2 if missing;
   that first-time runtime installation needs internet access.
3. Use **Overview** to confirm the watch is connected. Sign in under **Garmin
   Connect** if you want cloud data and sync. For an AI assistant, open **AI &
   MCP** and copy the connection URL or Claude Code command.

The package includes the desktop application and CLI. Updates are installed
with a newer package. Windows releases use a personal self-signed identity;
acceptance with Smart App Control has been verified on the development
machine, but is not guaranteed on other computers. Native Linux USB still
needs a physical-device check. See [installation and uninstall details](docs/desktop.md),
[MCP setup](docs/mcp.md), and [validation limits](docs/desktop-validation.md).

### CLI only

For the CLI alone, download a pre-built Windows or Linux binary (amd64 or arm64) from the
[releases](https://github.com/DevWolk/garminctl/releases), unpack it, and put
`garminctl.exe` (Windows) or `garminctl` (Linux/WSL) on your `PATH`.
`checksums.txt` beside the archives contains SHA-256 sums.

The standalone archives are portable, CGO-disabled CLI builds. They support
Windows/WPD access, including from WSL, and directory dumps. For direct USB on
native Linux, use the libusb-enabled CLI included in the DEB/RPM package;
installing libusb alone does not add USB support to the portable archive.

With a Go toolchain, install the latest release or a specific version:

```sh
go install github.com/DevWolk/garminctl/cmd/garminctl@latest
go install github.com/DevWolk/garminctl/cmd/garminctl@v0.1.0
```

For a source build with native Linux USB, install `pkg-config` and
`libusb-1.0-0-dev` first and enable CGO. These commands install the CLI;
the desktop application is included in the native installer.

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
- **Desktop app:** [installation, lifecycle, and supported platforms](docs/desktop.md).
- **Web UI:** [tabs, filtering, and browser behavior](docs/web-ui.md).
- **MCP server:** [setup and tool list](docs/mcp.md).
- **Garmin Connect:** [sign-in, sync, and automatic USB sync](docs/cloud-sync.md).
- **Garmin Express parity:** [release watcher and latest assessment](docs/express-parity.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE).
