# Contributing

Changes must pass `make check` and preserve the codec fixture tests.
The [Make targets](.make/) and [CI workflows](.github/workflows/) define the
build and validation commands.

Read [how garminctl works](docs/architecture.md) first: it explains the code
layout, the import rules that the tests enforce, and the reasons for them.

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
and the tags `desktop,production,gtk3`.

Unit tests use repository fixtures without a watch. Use `-root DIR` for offline
commands, where `DIR` contains a copy of `GARMIN/`. The repository has no
ready-made directory for this: the tests build one in a temporary directory
from [`internal/ship/testdump`](internal/ship/testdump). To try commands by
hand, copy a watch once with `garminctl backup DIR`. See
[file formats](docs/formats.md) before changing codecs, device settings or
transports.

A local build has no Garmin Connect key pair built in and downloads it at
sign-in; see [the Garmin Connect key](docs/releases.md#configure-the-garmin-connect-key).

## Build and validation

Run `make help` for the complete target list. Build and quality targets start
the shared container as needed; `make bash` opens its shell. Compose provides
the toolchain without publishing an application port or passing through USB.

| Target | Result |
| --- | --- |
| `make all` | Linux/WSL and Windows CLIs, plus Linux and Windows desktop binaries in `bin/`. |
| `make build-linux-usb` | Native libusb CLI at `bin/garminctl-linux`; the portable `bin/garminctl` has CGO disabled. |
| `make package VERSION=v1.2.3` | Windows Setup and Linux amd64 DEB/RPM packages in `dist/`, using the chosen version. See [releases](docs/releases.md). |
| `make check` | Module consistency, pinned linters including govet and reachable-vulnerability checks for Linux and Windows code, race tests, package-tool and package-runner tests, and UI tests. |
| `make test`, `make race` | Go tests, with race detection and shuffled order for `race`. |
| `make lint-fix`, `make fmt` | Available linter fixes, or Go formatting. |
| `make test-windows` | Compile tests in Docker and execute them on a Windows host through PowerShell, using checkout fixtures. |
| `make install`, `make update` | Rebuild the binaries, restart a server that runs as `./bin/garminctl serve` from the checkout, and refresh the CLI copy of `garminctl autostart on` when autostart is on; `update` first pulls the current branch with `--ff-only`. |

`make install` preserves stopped servers and does not update an application
installed by Setup/DEB/RPM. Build and install a package to check that installation;
then [check its MCP connection](docs/mcp.md#check-the-connection).

To try a build from the checkout without a watch:

```sh
bin/garminctl -root DIR info
bin/garminctl -root DIR serve
bin/garmin-desktop -addr 127.0.0.1:8791 -root DIR
```

To run part of the tests, open the container and use `go test` with the build
tags of the gate:

```sh
make bash
go test -tags desktop,production,gtk3 ./tests/architecture/ ./tests/contracts/...
go test -tags desktop,production,gtk3 ./internal/containers/music/...
```

On native Linux, `bin/garminctl` has no USB support; `make build-linux-usb`
builds `bin/garminctl-linux`. Without the DEB or RPM package, install the USB
access rule yourself: copy `build/linux/69-garminctl.rules` to
`/etc/udev/rules.d/`, run `sudo udevadm control --reload` and reconnect the
watch.

The Windows test runner accepts `WINDOWS_TEST_CERT_THUMBPRINT` for signing
test binaries with an existing `CurrentUser\My` certificate. Blocked executables
remain failed checks; the runner continues with the remaining packages.

## Change requirements

- Keep FIT, Connect IQ settings and app-registry round-trip tests passing.
  Add a suitable fixture when supporting a new format; review it for personal data.
- Fix linter findings. A necessary `//nolint` names its linter and explains
  the reason on the same line; [.golangci.yml](.golangci.yml) defines the rules.
- Write device settings through `settings.ApplyAction`; preserve the
  [settings-change protocol](docs/formats.md#settings-change-files).
- Keep dependencies small and discuss additions before adding a module.
- Keep each pull request focused, explain its effect, and report validation.
  The Linux/Windows tests, quality gate and installed-package checks must pass.
  CI runs them on a pull request. A pull request that changes only Markdown
  files or `docs/` does not start them.

## Documentation

Update the guides in the same pull request as the behavior:

| Change | Update |
| --- | --- |
| CLI command or flag | The usage text in `internal/app/cli/cli.go` and [docs/cli.md](docs/cli.md). |
| HTTP route | The pinned list in `tests/contracts/http_routes_test.go` and [docs/http-api.md](docs/http-api.md). |
| MCP tool | [docs/mcp.md](docs/mcp.md); the contract tests pin the tool definitions. |
| UI tab or label | [docs/web-ui.md](docs/web-ui.md), and any guide that quotes the label. |
| File format or transport | [docs/formats.md](docs/formats.md). |
| Package layout or import rule | [docs/architecture.md](docs/architecture.md) and `tests/architecture`. |

Write for a reader who has not seen the code:

- Say what the reader can do, then how. Use numbered steps for a procedure and
  a table for a lookup.
- Use short sentences and the words of the interface. Quote UI labels in
  bold, exactly as shown.
- Explain a term where it first appears, or add it to the
  [glossary](docs/glossary.md).
- Document supported behavior and its limits. Keep the text device-neutral,
  and keep local test reports out of the guides.

## Copilot usage

Do not automatically request Copilot reviews on pull requests. Keep normal
test, lint, security and package workflows enabled.

The Garmin Express watch workflow can create a Copilot coding task only when
it detects a new upstream release and `EXPRESS_WATCH_TOKEN` is available.
The release check and metadata pull request run independently of this task.

| Trigger | Copilot control | Default |
| --- | --- | --- |
| Manual Garmin Express watch run | Select the `copilot` input for that run. | Off |
| Scheduled Garmin Express watch run | Set the repository Actions variable `EXPRESS_WATCH_COPILOT` to `true`. | Off when unset or any other value |
| Ordinary pull request | No workflow requests a Copilot review or coding task. | Off in workflows |

GitHub can also request reviews through personal Copilot settings or repository
rulesets. Disable **Automatic Copilot code review**, **Review new pushes** and
draft reviews in personal settings, and remove **Automatically request Copilot
code review** from any applicable rulesets. These settings are separate from
workflow triggers; see [GitHub's configuration guide](https://docs.github.com/en/copilot/how-tos/copilot-on-github/set-up-copilot/configure-code-review).

`copilot-setup-steps.yml` prepares the agent environment. It does not request
a review or start a coding task, and has no pull-request trigger.
