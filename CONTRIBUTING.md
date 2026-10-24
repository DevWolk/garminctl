# Contributing

garminctl is a small Go project; a change should build, pass `make check`
and keep the byte-exact codec tests green.

## Prerequisites

- Docker Engine with Compose v2, and `make`. The Go 1.27.3, GTK/WebKit,
  libusb, NSIS and Linux packaging tools run in the development container.
- `make image-build` creates the toolchain image. `make up` starts the shared
  container, `make bash` opens its shell, and `make down` removes it.
- `.make/*.mk` groups targets by purpose, following DevWolk/scenario's layout.
  `make help` lists them; optional `.make/options.local.mk` overrides local
  settings and is ignored by Git. Host UID/GID are passed to Compose, so `bin/`,
  `dist/` and `.cache/` remain owned by the developer.
- golangci-lint and govulncheck remain pinned in `tools.mod`; `make tools-update`
  updates them. CI runs the same container and Make targets as local development.
- For direct CLI development without Docker, install Go from `go.mod`;
  native Linux USB also needs `pkg-config` and `libusb-1.0-0-dev`. Desktop
  compilation additionally needs GTK 3 and WebKitGTK 4.1 development headers
  and the `desktop,production,webkit2_41` build tags.
- A watch is needed only for live tests; everything else runs against a
  dump (`-root DIR`, a copy of the watch's `GARMIN/` folder) or the fixtures
  under `*/testdata`.

## Build, test, lint

```sh
git clone git@github.com:DevWolk/garminctl.git
cd garminctl
make image-build
make all        # portable CLI and desktop binaries for Linux and Windows
make package    # Windows Setup, Linux DEB and RPM in dist/
make check      # golangci-lint (govet included), go test -race, govulncheck
make lint-fix   # apply the auto-fixable findings and formatting
```

`bin/garminctl` is built with CGO disabled so it runs in WSL without libusb.
`make build-linux-usb` creates `bin/garminctl-linux`, which Linux packages install
as `/usr/bin/garminctl` with the libusb runtime dependency. Both share the same
commands and service layer.

## Make target reference

Run `make` or `make help` to list targets without starting Docker. Build and
quality targets start the shared container as needed. The container provides
the toolchain; the resulting application runs on the host OS. Compose does
not publish an application port or pass a USB watch through to the container.

| Targets | Purpose or output |
| --- | --- |
| `make image-build` | Build the pinned development/toolchain image. |
| `make up`, `make bash`, `make ps` | Start the shared container, open a shell, or inspect its status. |
| `make stop`, `make down` | Stop the container, or remove it while keeping local caches. |
| `make cache-init`, `make deps` | Create local output/cache directories, or download the pinned dependencies. |
| `make build` | Portable, CGO-disabled CLI: `bin/garminctl`. |
| `make build-linux-usb` | Native Linux USB CLI: `bin/garminctl-linux`. |
| `make build-windows` | Windows CLI: `bin/garminctl.exe`. |
| `make build-desktop`, `make build-desktop-windows` | Native app: `bin/garmin-desktop` or `bin/garmin-desktop.exe`. |
| `make all` | Portable CLI, Windows CLI, and both desktop binaries. |
| `make assets` | Generate application icons and Windows executable resources. |
| `make package`, `make package-linux`, `make package-windows` | All installers, DEB/RPM only, or Windows Setup only, under `dist/`. |
| `make test`, `make race` | Run Go tests including desktop packages, with race detection for `race`. |
| `make tidy` | Check module metadata without modifying files. Included in `make check`. |
| `make test-windows` | Compile test binaries in Docker, then run the full suite on the Windows host through PowerShell (Windows/WSL only). |
| `make lint`, `make vet`, `make vuln` | Run pinned linters, go vet, or reachable-vulnerability analysis. |
| `make lint-fix`, `make fmt` | Apply linter fixes or format the Go source. |
| `make check`, `make quality` | Complete required lint/race/vulnerability gate; `quality` is an alias. |
| `make ci` | Build the image, fetch dependencies, and run the quality gate. |
| `make tools-update` | Update the versions pinned in `tools.mod` and `tools.sum`. |
| `make install` | Rebuild and refresh the existing development server/Windows background CLI copy. Leaves stopped servers stopped. |
| `make update` | Run `git pull --ff-only`, then `make install`, on the current checkout's branch. |
| `make clean` | Remove the five CLI/desktop binary outputs from `bin/`. |

Set `VERSION` explicitly when building release artifacts, for example
`make package VERSION=v0.5.0`. Otherwise it comes from `git describe` and can
include a commit ID and `dirty`. Building packages does not publish a release.

PR checks upload a `garminctl-packages` Actions artifact and install those
packages on Windows and in clean Ubuntu/Fedora userspaces. The installed native
window, CLI and HTTP MCP are exercised against repository fixtures. Tag releases
use the same pipeline and publish its tested assets only after all installation
jobs pass. Signing credentials are configured as described below and are never
needed by PR checks.

Docker here runs Linux userspace and cross-compiles Windows binaries. It does
not supply a Windows/macOS kernel or their native desktop APIs; see Docker's
[platform explanation](https://docs.docker.com/build/building/multi-platform/).
`make check` runs the full Linux gate, including `go mod tidy -diff`.
`make test-windows` runs the compiled test packages on the actual Windows host,
with package working directories pointing to this checkout so fixtures load.
It requires Windows PowerShell through WSL or Git Bash. No physical watch is
used by the unit tests. Test helpers launch without creating console windows.

On a Windows machine with Smart App Control, freshly compiled test executables
can be blocked. Optionally set `WINDOWS_TEST_CERT_THUMBPRINT` to an existing
code signing certificate in `CurrentUser\My`; the runner signs test binaries
with that identity without installing trust roots or changing protection.
Self-signing does not guarantee acceptance. A blocked package remains a failed
check and is reported; other packages are still attempted. GitHub Windows
tests remain necessary alongside this local target. macOS is not currently a
desktop release target.
The tag workflow runs `make check`, builds native installers, generates
`installer-checksums.txt`, and attaches them alongside the CLI archives.
See [Windows signing and releases](docs/releases.md) for unsigned personal
builds, signing options, workflow triggers, and the publication sequence.

`make install` and `make update` do not replace the native application installed
by Setup/DEB/RPM. To test the installed app, build its package and install that
package through the OS; use the [installed-app MCP procedure](docs/mcp.md#verify-the-installed-app).
For a checkout-only smoke check, launch `bin/garmin-desktop` (or `.exe`) with
`-addr 127.0.0.1:8791 -root /path/to/dump`.

## Rules of the road

- Read `docs/formats.md` before touching `internal/fit`, `internal/ciq`,
  `internal/garmin` or the WPD bridge; it records what the watch actually
  does and which facts were verified on the device.
- Every file format has a round-trip test; a decoded file must re-encode
  byte for byte. Add a sample to the package's `testdata/` for any new file
  type. The samples come from the author's own watch and carry his profile
  data, as the README says; do not add files from anyone else's watch.
- `.golangci.yml` is the source of truth for the linters. Fix findings
  rather than silence them; a `//nolint` names its linter and gives the
  reason on the same line.
- Device settings never rewrite `GARMIN/Settings/Settings.fit`; they go
  through a settings-change file in `GARMIN/NewFiles`.
- Keep the dependency list short. Ask before adding a module.

## Pull requests

One change per pull request, with the user-visible effect in the first line
of the description. CI runs the tests on Linux and Windows, the linter and
govulncheck; all of them must pass. CodeQL joins them once the repository
is public (code scanning is not available to a personal private one).
