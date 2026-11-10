# Desktop application

The native app includes the CLI and starts its web and MCP server automatically.
Download a package from [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest).

## Install and launch

| System | Package | Installation |
| --- | --- | --- |
| Windows 10+, x64 | `*_windows_amd64_setup.exe` | Run Setup, then launch **garminctl** from Start or the desktop shortcut. |
| WSL | Windows Setup above | Install and launch on Windows. An existing WSL CLI can launch it with `garminctl desktop`. |
| Ubuntu 24.04+, x64 | `*_linux_amd64.deb` | Run `sudo apt install ./garminctl_*.deb`, then launch from the application menu. |
| Fedora with WebKitGTK 4.1, x64 | `*_linux_amd64.rpm` | Run `sudo dnf install ./garminctl_*.rpm`, then launch from the application menu. |

Windows Setup installs WebView2 when absent; this requires internet access.
Its default installation directory is `%LOCALAPPDATA%\Programs\garminctl`.
Setup does not add the CLI to `PATH`. In PowerShell:

```powershell
& "$env:LOCALAPPDATA\Programs\garminctl\garminctl.exe" version
& "$env:LOCALAPPDATA\Programs\garminctl\garminctl.exe" desktop
```

Use your chosen directory if it differs. WSL and Windows have separate CLI
installations. Linux packages install `/usr/bin/garmin-desktop`,
`/usr/bin/garminctl`, and the required libraries. Reconnect the watch after
installation so the Garmin USB access rule applies.

Windows packages can be unsigned or signed according to the release configuration.
A personal self-signed certificate does not establish public trust or guarantee
Windows acceptance. See [release signing](releases.md).

## USB access

Windows and WSL use Windows Portable Devices (WPD). Native Linux packages use
libusb/MTP. Native Linux USB remains experimental and has not been verified on
physical Garmin hardware; device compatibility is not established across models.

The portable CLI archives support Windows/WSL and directory dumps. Direct Linux
USB requires the CLI in the DEB/RPM package or a CGO-enabled source build.
See [CLI installation and device selection](cli.md).

## Window and server

Launch the app and connect the watch. **Overview** shows the connected device,
storage, and pending changes. The [tab guide](web-ui.md) covers available actions;
[Garmin Connect](cloud-sync.md) describes automatic sync.

The app starts a server on `127.0.0.1:8787` or reuses a compatible garminctl
server already at that address. An unrelated or incompatible server causes a
startup error. When the window exits, a server it started and its background
sync stop; a reused server continues running.

Launching the app again focuses its existing window. The View menu provides
Back, Reload, **AI & MCP**, and Quit. Reload and window exit ask before
discarding unapplied edits. Downloads use a native Save dialog; external links
open the default browser. The installer does not enable launch at sign-in.

## Launch options

`garminctl desktop` accepts no arguments. Invoke the desktop executable for a
custom port or an offline directory:

```sh
garmin-desktop -addr 127.0.0.1:8791 -root ./watch-dump
```

On Windows, run `garmin-desktop.exe` from the installation directory.

| Flag | Default | Usage |
| --- | --- | --- |
| `-addr IP:PORT` | `127.0.0.1:8787` | Choose a loopback IP and port. |
| `-root DIR` | USB device | Use a watch directory or dump and disable automatic USB watching. |

For `-device` selection or `-autosync=false`, start a [CLI server](cli.md#servers)
with those options, then launch the app at the same address to reuse it.

## Data, updates, and uninstall

File exports are cancelled when the window closes and have a ten-minute
transfer timeout. If an export fails or is cancelled, an existing destination
file is retained; only a completed download replaces it.

| Data | Windows default | Linux/WSL default |
| --- | --- | --- |
| Connect tokens | `%APPDATA%\garminctl\garmin-connect.json` | `~/.config/garminctl/garmin-connect.json` |
| Caches and registry backups | `%LOCALAPPDATA%\garminctl` | `~/.cache/garminctl` |
| Desktop WebView2 profile | `%APPDATA%\garminctl\desktop` | Not used |

Linux paths respect `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`. Windows and WSL have
separate data directories, so switching between them can require another
Connect sign-in.

Before staging an app-list visibility change, garminctl saves and reads back
`OUT.BIN`, and the pending `IN.BIN` when one exists, in a unique
`OUT.BIN-<timestamp>-<suffix>/` snapshot under the cache directory. Before
rewriting a Connect IQ app's settings it saves the current file in
`<NAME>.SET-<timestamp>-<suffix>/<NAME>.SET`. Directory/file access is
restricted to the current user. If a backup fails, the change is not written.
Successful copies remain available even if the subsequent device write fails;
nothing removes them automatically, and cache removal also removes these
recovery copies.

### Update an installed package

Choose **Help → Application updates…** to open **Application**, or open
[GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest).
Download the newer package for the same system and architecture, then quit
garminctl before installing it. Stop any separate `garminctl serve` process too.

| Installation | Update |
| --- | --- |
| Windows/WSL desktop | Run the new Setup using the existing installation directory. |
| Ubuntu DEB | Run `sudo apt install ./garminctl_NEW_VERSION_linux_amd64.deb`. |
| Fedora RPM | Run `sudo dnf upgrade ./garminctl_NEW_VERSION_linux_amd64.rpm`. |
| Portable CLI archive | Replace the existing CLI binary with the new one. |
| CLI installed with Go | Run `go install github.com/DevWolk/garminctl/cmd/garminctl@latest`. |

Replace `NEW_VERSION` with the downloaded version. Packages preserve the data
locations above. Reopen garminctl after updating; a running server must restart
to use the new binary. MCP configuration can stay unchanged when its URL or
executable path stays the same.

**Application** shows separate desktop, CLI and backend versions, local platform,
and the detected installation type. Unknown ownership and differing versions
remain visible. **Check for updates** refreshes release history from GitHub;
stable releases are selected by default. **Include prereleases** changes which
saved releases are shown without another network request. Saved history includes
the time of its last successful check and remains available offline. Release
notes are displayed as text. The Releases link opens the browser for manual
package download. In builds with a trusted verification key, **Download selected
release** verifies and saves the package locally. **Cancel download** waits for
worker cleanup; reloading the page resumes observation of the same operation.
Preparation requires confirmed installation ownership and matching desktop/CLI
builds. A missing key or an unsupported installation is reported explicitly.
This page does not install updates.
The **Updates** tab manages watch software and apps. The GitHub packages do not
configure an APT/DNF repository, so system updates alone will not fetch newer
versions of garminctl.

### Uninstall

Uninstall through
Windows Installed Apps (**garminctl Desktop**), `sudo apt remove garminctl`, or
`sudo dnf remove garminctl`. Uninstall preserves configuration and caches.

Sources: [installer](../build/windows/installer.nsi),
[Linux packages](../scripts/package-linux.sh),
[desktop lifecycle](../internal/desktop/backend.go),
[launch options](../cmd/garmin-desktop/main.go).
