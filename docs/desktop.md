# Desktop application

As of 2026-10-24, native installers have been built locally but have not yet
been published in a tagged release. See [validation status](desktop-validation.md)
for completed checks and remaining limits. Once published, download the
installer from [GitHub Releases](https://github.com/DevWolk/garminctl/releases):

| System | File | Installation |
| --- | --- | --- |
| Windows 10/11, x64 | `*_windows_amd64_setup.exe` | Open Setup, install, launch garminctl from Start or the desktop shortcut. |
| WSL | Windows Setup above | Install on Windows and launch from Start. An installed WSL CLI can also launch it with `garminctl desktop`. |
| Ubuntu 24.04+, x64 | `*_linux_amd64.deb` | Open with the system package installer, or `sudo apt install ./garminctl_*.deb`. |
| Fedora with WebKitGTK 4.1, x64 | `*_linux_amd64.rpm` | Open with the system package installer, or `sudo dnf install ./garminctl_*.rpm`. |

The package includes the native application and CLI. ARM64 CLI archives remain
available separately; native installers currently target x64. Docker is only a
build dependency, and is not required on the user's machine.

The Windows installer uses a per-user directory, creates shortcuts, and adds
an entry to Installed Apps for uninstalling. It installs Microsoft's WebView2
runtime when absent (this requires internet access). Windows signing is
configurable; [personal signing tests](windows-selfsign-test.md) successfully
installed and launched the app on this machine with Smart App Control enabled.
Self-signing does not establish public trust or guarantee acceptance on other
machines. See [signing and release setup](releases.md) for signed and unsigned
builds. Linux packages declare GTK/WebKitGTK/libusb dependencies
and install a Garmin-only udev rule; reconnect the watch after installation. Close any file
manager connection to the watch before connecting garminctl.

## Install and launch

On Windows, open `garminctl_<version>_windows_amd64_setup.exe`, choose the
installation directory, and finish Setup. The default directory is
`%LOCALAPPDATA%\Programs\garminctl`. Launch the **garminctl** Start menu entry
or desktop shortcut. Setup includes `garminctl.exe` but does not add it to
`PATH`; at the default location, use these commands in PowerShell:

```powershell
& "$env:LOCALAPPDATA\Programs\garminctl\garminctl.exe" version
& "$env:LOCALAPPDATA\Programs\garminctl\garminctl.exe" desktop
```

If you chose another installation directory, use that path instead. To call
`garminctl` by name, add its directory to your user `PATH`.

On WSL, install the Windows Setup using Windows. It installs the application
and CLI on Windows; it does not install a Linux CLI into your WSL distribution.
Launch the app from Start. If the portable Linux CLI is already on WSL's
`PATH`, `garminctl desktop` finds and starts the Windows installation.

On Ubuntu, install the downloaded package with dependencies:

```sh
sudo apt install ./garminctl_<version>_linux_amd64.deb
garminctl desktop
```

On Fedora:

```sh
sudo dnf install ./garminctl_<version>_linux_amd64.rpm
garminctl desktop
```

Replace `<version>` with the downloaded filename's version. Both Linux packages
install `/usr/bin/garmin-desktop`, `/usr/bin/garminctl`, and an application-menu
entry. You can use the menu instead of the terminal. Reconnect the USB watch
after installation so the udev access rule applies.

## First use and available features

1. Launch the installed application and connect the watch by USB. Overview
   shows its model, firmware, pending files, and storage. The default USB
   watcher connects automatically; the Connect button can also open the watch.
2. Use Watch settings, App settings, Apps & glances, Files, and Updates to
   manage the watch. Health & activities, Workouts, Music, and Store provide
   the corresponding data and actions. See the [complete tab guide](web-ui.md).
3. Sign in under Garmin Connect for cloud health data, workouts, and sync.
   Automatic USB sync is enabled by default and can upload recordings and
   write items queued by Connect to the watch. See [sync behavior](cloud-sync.md).
4. Open AI & MCP to connect an assistant to this running application. Copy its
   URL or Claude Code command; sessions and tool calls appear in the same tab.
   Follow the [installed-app MCP check](mcp.md#verify-the-installed-app).

Device settings go through `GARMIN/NewFiles`; app-registry changes go through
`GARMIN/Apps/IN.BIN`. Both are applied when the watch is unplugged. App `.SET`
settings take effect immediately. The UI offers a review step for pending
settings edits.

## Window and server lifecycle

Wails v2 hosts the existing UI in its own native window. No browser installation
or PWA setup is needed. The application starts its local server automatically
on `127.0.0.1:8787`, or reuses an existing server with a compatible desktop API.
An unrelated or older server produces an error window explaining which port to
free. Closing the app stops its own server and background sync; a pre-existing
server remains running. Opening the application again focuses its existing
window. There is no automatic launch at sign-in in the installer.

The View menu provides Back, Reload, AI & MCP, and Quit. Reload and closing the
window confirm discarding unapplied changes. Downloads use a native Save dialog;
external Garmin links open the system's default browser.

The server is still reachable at its loopback URL while the application runs.
The AI & MCP tab provides its `/mcp` URL, a Claude Code command, open sessions,
last activity timestamps, and the last 100 tool calls. See [MCP](mcp.md).

## Application command-line options

`garminctl desktop` launches the installed app without arguments. For a custom
port or offline dump, invoke the installed application executable directly:

```sh
garmin-desktop -addr 127.0.0.1:8791
garmin-desktop -addr 127.0.0.1:8791 -root /path/to/dump
```

On Windows, the equivalent is:

```powershell
& "$env:LOCALAPPDATA\Programs\garminctl\garmin-desktop.exe" -addr 127.0.0.1:8791
```

`-addr` requires a loopback IP and port and defaults to `127.0.0.1:8787`.
`-root` uses a directory instead of USB and disables the automatic watcher.
These are the desktop executable's two options; `-device` and `-autosync` are
CLI/server options. To select a particular watch or disable automatic sync,
start `garminctl -device ID serve -autosync=false` first, then open the desktop
app so it reuses that compatible server. Its lifetime then belongs to the
process that started it.

## Device access and data

Windows and WSL use the existing WPD bridge. Native Linux uses libusb and MTP,
selecting only Garmin USB vendor `091e`. Native Linux requires an explicit CLI
`-device` ID when more than one watch is present; Windows/WSL otherwise opens
the first matching Garmin. Hidden Connect IQ files use Garmin operation `0x9002`.
Transport tests cover packet boundaries, interruption, hidden files, replacing
files, and path validation. **Native Linux access still needs verification on
physical Garmin hardware**; current live-device evidence is for WPD on Windows.

Configuration, Garmin Connect tokens, and caches live in the OS user's data
folders and are preserved on uninstall. Windows and WSL have separate user data
folders; moving from a WSL server to the Windows application may require signing
in to Garmin Connect again. The existing WSL data is not deleted.

| Data | Windows default | Linux/WSL default |
| --- | --- | --- |
| Connect tokens | `%APPDATA%\garminctl\garmin-connect.json` | `~/.config/garminctl/garmin-connect.json` |
| Watch/bridge caches | `%LOCALAPPDATA%\garminctl` | `~/.cache/garminctl` |
| Native WebView2 profile | `%APPDATA%\garminctl\desktop` | Not used |

Linux paths follow `XDG_CONFIG_HOME` and `XDG_CACHE_HOME` when set. The app does
not save the Garmin Connect password.

## Update, uninstall, and replace a PWA

To update, close the application and run the newer Windows Setup, or install
the newer DEB/RPM with the commands above. Application updates use installers;
the **Updates** tab manages watch firmware/data and Connect IQ apps. There is
no desktop self-updater.

Uninstall Windows through Installed Apps → **garminctl Desktop**. On Ubuntu,
use `sudo apt remove garminctl`; on Fedora, use `sudo dnf remove garminctl`.
Configuration and caches are preserved.

If replacing an earlier browser PWA, use the browser's uninstall action for
that PWA, stop any manually started `garminctl serve`, and disable its Windows
background-server shortcut with `garminctl autostart off`. The native app then
starts its own server. An existing compatible server will instead be reused
and will continue after the window closes. The native installer does not
remove old PWA registrations or enable sign-in autostart.

## Build and validate

See [CONTRIBUTING](../CONTRIBUTING.md) for the Docker toolchain and Make targets,
and [validation status](desktop-validation.md) for completed checks and limits.
`make package` creates installers in `dist/`; `make check` covers desktop and
CLI code. Tagged releases run that gate, build installers and CLI archives,
exercise installed packages on Windows/Ubuntu/Fedora, then publish those exact
artifacts with `checksums.txt`. Pull requests produce downloadable test
artifacts without publishing a release. Building locally never publishes one.

For an isolated window over a dump, run
`bin/garmin-desktop -addr 127.0.0.1:8791 -root /path/to/dump` (or the `.exe` on
Windows). Do not run `make install` just to open a desktop build: it refreshes
existing development servers and Windows CLI autostart installations.
It does not update an application installed by Setup, DEB, or RPM; install a
new package to test an updated installed application.
