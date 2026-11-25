# Desktop app

The desktop app is garminctl in its own window. It shows the same
[tabs](web-ui.md) as the browser interface. Installing it also installs the
command-line program (the [CLI](cli.md)), and the app starts the
[local server](#the-local-server) that browsers and AI assistants connect to.

## Install

1. On Windows 11, check [Smart App Control](#smart-app-control) first.
2. Open [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest)
   and download the file for your system:

   | System | File name ends with | Install and open |
   | --- | --- | --- |
   | Windows 10 or later, 64-bit | `_windows_amd64_setup.exe` | Run it, then open **garminctl** from Start or the desktop shortcut. |
   | WSL | The Windows file above | Install and open it on Windows. A CLI inside WSL can open it with `garminctl desktop`. |
   | Ubuntu 24.04 or later, 64-bit | `_linux_amd64.deb` | `sudo apt install ./garminctl_*.deb`, then open it from the application menu. |
   | Fedora with WebKitGTK 4.1, 64-bit | `_linux_amd64.rpm` | `sudo dnf install ./garminctl_*.rpm`, then open it from the application menu. |

   The other files on that page are the CLI-only archives, checksums and
   signatures.
3. Connect the watch by USB. On Linux, reconnect it after the installation so
   that the new USB access rule applies.
4. Open **Overview**. It shows the watch and its storage. If it says **Not
   connected**, choose **Connect watch** at the bottom of the sidebar.

### Windows notes

- Setup installs WebView2, the Windows component that displays the app, when
  it is missing; this needs internet access.
- The default installation directory is `%LOCALAPPDATA%\Programs\garminctl`.
- Windows and WSL have separate CLI installations.
- A release's Windows files can be unsigned or signed, depending on how the
  release was built. A personal self-signed certificate does not establish
  public trust or guarantee that Windows accepts the files. See
  [release signing](releases.md#configure-windows-signing).

### Run the CLI on Windows

Setup does not add the CLI to `PATH`, so a command such as `garminctl info`
from these guides is not found. In PowerShell, either run the program by its
full path:

```powershell
& "$env:LOCALAPPDATA\Programs\garminctl\garminctl.exe" version
```

or define the short name for the current PowerShell window, then use the
commands as the guides print them:

```powershell
Set-Alias garminctl "$env:LOCALAPPDATA\Programs\garminctl\garminctl.exe"
garminctl version
```

Use your own directory if you changed it during Setup.

### Linux notes

The packages install `/usr/bin/garmin-desktop`, `/usr/bin/garminctl` and the
libraries they need.

## Smart App Control

[Smart App Control](https://support.microsoft.com/en-us/windows/security/threat-malware-protection/smart-app-control-frequently-asked-questions)
is a Windows 11 protection that runs an app only when Microsoft's cloud service
rates it safe or when it is signed with a certificate from a publicly trusted
authority. garminctl releases have neither, and Windows offers no exception for
a single app.

Microsoft's page, linked above, explains how to see whether it is on.

**With Smart App Control on, Windows can block `garminctl.exe`.** The CLI does
not start, the Application tab cannot check the installation or saved
packages, and in-app update and rollback report that the helper program was
blocked and change nothing. The desktop app itself may still run.

What this means in practice:

- Use the app's tabs. Commands in these guides that start with `garminctl` do
  not run.
- To update garminctl, install each release by hand with the Setup from
  [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest).

**Turning Smart App Control off is not recommended.**

## USB access

| System | How garminctl reaches the watch |
| --- | --- |
| Windows and WSL | Windows Portable Devices (WPD), which is part of Windows. |
| Linux | libusb and MTP, in the CLI from the DEB or RPM package or in a source build with CGO. |

Native Linux USB is experimental. It has not been verified on physical Garmin
hardware, and compatibility across watch models is not established.

The portable CLI archives support Windows, WSL and
[directory copies of a watch](cli.md#global-options), but not direct Linux USB.

## Use the app

Open the app and connect the watch. **Overview** shows the connected watch, its
storage and pending changes. The [tab guide](web-ui.md) covers what each tab
does.

When a watch is plugged in, the app connects to it and checks for watch
software updates. If you are signed in to Garmin Connect, it also syncs. See
[automatic USB sync](cloud-sync.md#automatic-usb-sync), also for turning this
off.

- Opening the app again brings the existing window to the front.
- The **View** menu has **Back**, **Reload**, **AI & MCP** and **Quit**.
  **Help → Application updates…** opens the **Application** tab.
- **Reload** and closing the window ask before discarding edits you have not
  saved.
- Downloads use a Save dialog. Links to websites open in your default browser.
- The app does not start by itself when you log in to your computer.

### Saving files

A file export stops when the window closes, and a transfer times out after ten
minutes. If an export fails or is cancelled, a file that already exists at the
destination is kept; only a completed download replaces it.

### The local server

The interface is served by a small web server inside garminctl, the local
server. It listens at `127.0.0.1:8787` by default, an address that only this
computer can reach. Usually the app starts the server and stops it when the
window closes. The exception is a server that is already running:

| What the app finds at that address | What it does |
| --- | --- |
| Nothing | Starts its own server. The server and its background sync stop when the window closes. |
| A server started with `garminctl serve` for the same watch | Uses it. That server keeps running after the window closes. |
| A `garminctl serve` that reads another watch or directory (`-device`, `-root`) | Starts its own server on a free port. |
| Another program, or an incompatible garminctl | Reports a startup error. Stop that program first. |

## Launch options

`garminctl -root DIR desktop` and `garminctl -device ID desktop` pass those
options to the app. For another port, run the desktop executable itself:

```sh
garmin-desktop -addr 127.0.0.1:8791 -root ./watch-dump
```

On Windows, run `garmin-desktop.exe` from the installation directory.

| Flag | Default | Use |
| --- | --- | --- |
| `-addr IP:PORT` | `127.0.0.1:8787` | A loopback IP address and port. |
| `-root DIR` | USB watch | Read a watch directory or copy, and turn off automatic USB detection. |
| `-device ID` | First Garmin device | Use this USB device. |

Quit an open app before starting it with other options.

To run the app without automatic sync, see
[turn automatic sync off](cloud-sync.md#turn-automatic-sync-off).

## Where garminctl keeps its data

| Data | Windows | Linux and WSL |
| --- | --- | --- |
| Configuration directory, `<config>` | `%APPDATA%\garminctl` | `~/.config/garminctl` |
| Garmin Connect sign-in | `<config>\garmin-connect.json` | `<config>/garmin-connect.json` |
| Cache directory, `<cache>`: caches and safety copies | `%LOCALAPPDATA%\garminctl` | `~/.cache/garminctl` |
| The window's browser profile | `<config>\desktop` | Not used |

- On Windows, paste a path such as `%APPDATA%\garminctl` into File Explorer's
  address bar to open it.
- The cache directory `%LOCALAPPDATA%\garminctl` is not the program directory
  `%LOCALAPPDATA%\Programs\garminctl`.
- Linux paths follow `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`.
- Windows and WSL have separate data directories, so you may need to sign in
  to Garmin Connect in each.
- A sign-in that waits for a verification code is saved beside the sign-in
  file; see [verification codes](cloud-sync.md#verification-codes).

### Safety copies of watch files

Before two kinds of change, garminctl saves the current watch file under the
cache directory and reads the copy back:

| Change | Copy |
| --- | --- |
| Hiding or showing an app | `OUT.BIN`, and the pending `IN.BIN` when one exists, in a unique `OUT.BIN-<timestamp>-<suffix>/` directory. |
| Changing a Connect IQ app's settings | The current file, as `<NAME>.SET-<timestamp>-<suffix>/<NAME>.SET`. |

- Only your user account can read these copies.
- If a copy fails, the change is not written.
- A copy stays even when the write to the watch fails afterwards.
- Nothing removes the copies automatically. Deleting the cache directory
  deletes them too.

The files kept for updating garminctl are listed in
[what garminctl keeps for updates](updating.md#what-garminctl-keeps-for-updates).

## Update

See [updating garminctl](updating.md): updating by hand, updating from the
**Application** tab, and returning to an older version. The **Updates** tab is
for watch software and watch apps.

## Uninstall

| System | How |
| --- | --- |
| Windows | **Installed apps** → **garminctl Desktop** → **Uninstall** |
| Ubuntu | `sudo apt remove garminctl` |
| Fedora | `sudo dnf remove garminctl` |

Uninstalling keeps your configuration and caches, including the saved Garmin
Connect sign-in. On Windows it also removes the startup shortcut and the copy
of the CLI that [`garminctl autostart on`](cloud-sync.md#start-the-server-when-you-log-in-to-windows)
created; a server that is still running keeps its copy until it exits.

To remove everything:

1. In the **Garmin Connect** tab, choose **Sign out**. This deletes the saved
   sign-in and its copies in the data backups.
2. Uninstall as above.
3. Delete the `<config>` and `<cache>` directories from
   [the table above](#where-garminctl-keeps-its-data).
4. If you connected an AI assistant, remove garminctl from its MCP settings.

Sources: [installer](../build/windows/installer.nsi),
[Linux packages](../scripts/package-linux.sh),
[desktop lifecycle](../internal/ship/desktop/backend.go),
[launch options](../cmd/garmin-desktop/main.go).
