# Contributing

Changes must pass `make check` and preserve the codec fixture tests.
The [Make targets](.make/) and [CI workflows](.github/workflows/) define the
build and validation commands.

## Development setup

Install Docker Engine with Compose v2 and `make`, then run:

```sh
git clone https://github.com/DevWolk/garminctl.git
cd garminctl
make image-build
make all
make check
```

The [toolchain image](docker/Dockerfile) supplies Go, GTK/WebKitGTK, libusb,
NSIS and Linux packaging tools. Compose uses the host UID/GID and stores outputs
and caches in `bin/`, `dist/` and `.cache/`. Optional
`.make/options.local.mk` overrides local settings and is ignored by Git.

For development without Docker, use the Go version in [go.mod](go.mod).
Native Linux USB builds need `pkg-config` and libusb development headers.
Linux desktop builds also need GTK 3 and WebKitGTK 4.1 development headers
and the tags `desktop,production,webkit2_41`.

Unit tests use repository fixtures without a watch. Use `-root DIR` for offline
commands, where `DIR` contains a copy of `GARMIN/`. See [file formats](docs/formats.md)
before changing codecs, device settings or transports.

## Build and validation

Run `make help` for the complete target list. Build and quality targets start
the shared container as needed; `make bash` opens its shell. Compose provides
the toolchain without publishing an application port or passing through USB.

| Target | Result |
| --- | --- |
| `make all` | Linux/WSL and Windows CLIs, plus Linux and Windows desktop binaries in `bin/`. |
| `make build-linux-usb` | Native libusb CLI at `bin/garminctl-linux`; the portable `bin/garminctl` has CGO disabled. |
| `make package VERSION=v1.2.3` | Windows Setup and Linux amd64 DEB/RPM packages in `dist/`, using the chosen version. See [releases](docs/releases.md). |
| `make check` | Module consistency, pinned linters including govet, race tests and reachable-vulnerability checks, including desktop code. |
| `make test`, `make race` | Go tests, with race detection and shuffled order for `race`. |
| `make lint-fix`, `make fmt` | Available linter fixes, or Go formatting. |
| `make test-windows` | Compile tests in Docker and execute them on a Windows host through PowerShell, using checkout fixtures. |
| `make install`, `make update` | Rebuild the development server and Windows autostart CLI copy; `update` first pulls the current branch with `--ff-only`. |

`make install` preserves stopped servers and does not update an application
installed by Setup/DEB/RPM. Build and install a package to check that installation;
then [check its MCP connection](docs/mcp.md#http-connection).
For a checkout smoke check, run the desktop binary with
`-addr 127.0.0.1:8791 -root DIR`.

The Windows test runner accepts `WINDOWS_TEST_CERT_THUMBPRINT` for signing
test binaries with an existing `CurrentUser\My` certificate. Blocked executables
remain failed checks; the runner continues with the remaining packages.

## Change requirements

- Keep FIT, Connect IQ settings and app-registry round-trip tests passing.
  Add a suitable fixture when supporting a new format; review it for personal data.
- Fix linter findings. A necessary `//nolint` names its linter and explains
  the reason on the same line; [.golangci.yml](.golangci.yml) defines the rules.
- Write device settings through `ApplyChanges`; preserve the
  [settings-change protocol](docs/formats.md#settings-change-files).
- Keep dependencies small and discuss additions before adding a module.
- Keep each pull request focused, explain its effect, and report validation.
  The Linux/Windows tests, quality gate and installed-package checks must pass.
