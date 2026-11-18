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

## Smart App Control

[Smart App Control](https://support.microsoft.com/en-us/windows/security/threat-malware-protection/smart-app-control-frequently-asked-questions)
is a Windows 11 protection that runs an app only when Microsoft's cloud service
rates it safe or when it is signed with a certificate from a publicly trusted
authority. garminctl releases have neither, and Windows offers no exception for
a single app.

**With Smart App Control on, Windows can block `garminctl.exe`.** The CLI does
not start, the Application tab cannot check the installation or saved
packages, and in-app update and rollback report that the helper program was
blocked and change nothing. The desktop app itself may still run.

**Turning Smart App Control off is not recommended.** Install each release by
hand with the Setup from [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest)
instead.

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
server already at that address that reads the same watch or directory
(`-root`, `-device`). A garminctl server for other data makes the app start its
own on a free port; an unrelated or incompatible server causes a startup error. When the window exits, a server it
started and its background sync stop; a reused server continues running.

Launching the app again focuses its existing window. The View menu provides
Back, Reload, **AI & MCP**, and Quit. Reload and window exit ask before
discarding unapplied edits. Downloads use a native Save dialog; external links
open the default browser. The installer does not enable launch at sign-in.

## Launch options

`garminctl -root DIR desktop` and `garminctl -device ID desktop` pass those
options to the app. Invoke the desktop executable for a custom port:

```sh
garmin-desktop -addr 127.0.0.1:8791 -root ./watch-dump
```

On Windows, run `garmin-desktop.exe` from the installation directory.

| Flag | Default | Usage |
| --- | --- | --- |
| `-addr IP:PORT` | `127.0.0.1:8787` | Choose a loopback IP and port. |
| `-root DIR` | USB device | Use a watch directory or dump and disable automatic USB watching. |
| `-device ID` | First Garmin device | Use this USB device. |

An open app only comes to the front when launched again; quit it before
launching it with other options. For `-autosync=false`, start a
[CLI server](cli.md#servers) with that option, then launch the app at the same
address to reuse it.

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
Windows Setup also updates the server copy made by `garminctl autostart on`;
a copy in use by a running server keeps its old version.

| Installation | Update |
| --- | --- |
| Windows/WSL desktop | Run the new Setup using the existing installation directory. |
| Ubuntu DEB | Run `sudo apt install ./garminctl_NEW_VERSION_linux_amd64.deb`. |
| Fedora RPM | Run `sudo dnf upgrade ./garminctl_NEW_VERSION_linux_amd64.rpm`. |
| Portable CLI archive | Follow the [standalone CLI replacement](cli.md#update-a-standalone-cli) in its own directory. |
| CLI installed with Go | Use the [separate source CLI flow](cli.md#update-a-standalone-cli) with an explicit `GOBIN`. |

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
**Install saved release…** verifies both the selected update and a retained
package of the current version, saves a protected data backup and asks before
handoff. Active watch jobs or a separately owned backend prevent preparation.
Cancellation is available before handoff and waits for cleanup. Once the system
installer takes ownership, its outcome must be checked after restart; an accepted
handoff does not prove successful installation. An installation that did not
complete is shown in **Application** on the next start and by
`garminctl application status`, with the directory of its logs.
DEB and RPM updates ask once for system authorization (`pkexec`) to run the
installed `garminctl`, which verifies the package again before the package
manager installs it. Missing system tools, or on Windows a running
`garminctl.exe`, are reported before the application closes.
The **Updates** tab manages watch software and apps. The GitHub packages do not
configure an APT/DNF repository, so system updates alone will not fetch newer
versions of garminctl.

### Restore an older version and its data

In **Application**, select an older saved version and its matching data backup
under **Return to an older version**. **Prepare rollback review…** verifies the
packages and saves a fresh copy of current data. It does not restore or install
yet. To preserve current files, first select **Keep current data (requires
compatibility check)**. Preparation requires explicit matching format
provenance in both backups and checks the current account, MFA and upload-ledger
reader formats. Missing, unknown or incompatible provenance refuses this mode;
it never silently switches to restoring the older copy.

Review the selected mode, the two backup IDs and any file replacement/removal, then
check the explicit consent box and choose **Restore backup and install older
version** or **Keep current data and install older version**. Cancel keeps the
current files and waits for preparation cleanup. Confirmed work cannot be
canceled; a request to close the window waits until it ends.

Explicit restoration replaces the listed files with the selected backup; newer
changes to them remain only in the fresh safety copy. The compatible mode makes
no data replacements and rechecks current files after backend shutdown before
installer handoff. Other files, browser data and retained packages stay in place.
Reload resumes the same review and never confirms it automatically.

Restoration across files is not atomic. A failure may leave a partial data set
or an uncertain installer outcome; the dialog states whether local data is
unchanged, partly restored or fully restored. After a partial or full restore
do not repeat the operation; retain both
backup IDs and the packages for manual recovery. The GUI-independent
`garminctl application recovery snapshots` lists backups and
`garminctl application recovery inspect ID` checks an explicit copy.
`garminctl application recovery list` lists retained packages;
`garminctl application recovery export ID` verifies and exports a manual repair
package. These CLI commands do not restore data or run an installer.

### What garminctl keeps for updates

Updates and rollbacks keep the files below, private to the current user.
`<config>` is the directory of the Connect tokens and `<cache>` the cache
directory from the [table above](#data-updates-and-uninstall).

| Directory | Contents | Kept |
| --- | --- | --- |
| `<config>/application-backups/` | Data backups, one saved before each installation and each rollback review: copies of the Connect token and pending-verification files, upload ledgers and the watch recovery copies. | The 10 newest. |
| `<cache>/application-packages/` | Downloaded packages with their signatures. | The 5 most recently saved, and always the package of the installed version. |
| `<cache>/application-recovery/` | Copies made by `application recovery export`. | Until you delete them. |
| `<cache>/application-staging/` | Working copies of a download or a verification. | Removed when the operation ends. |
| `<cache>/application-installing/` | The package, a copy of the CLI and the log of an installation. | Removed when an installation is canceled. After a handoff to the system installer, the operation with the latest recorded result. |
| `<cache>/application-history/` | The release list of the last successful check. | Replaced by the next successful check. |

The counts are fixed. Once the installer accepts an update, the backups beyond
the newest 10 are deleted; an update that is refused or canceled and a rollback
delete none, so a retry does not push out older backups and the backup selected
for a rollback stays. A download that saves a new package deletes the packages beyond
the limit, and of two saved copies of one release it keeps the later one. A
working copy left by an interrupted operation is deleted by the next download,
update or installation once it has been unchanged for a day.

Signing out of Garmin Connect, in the app or with `garminctl cloud logout`,
deletes the token and pending-verification copies from every backup. The
backups stay valid for their other files.

A saved package or backup that this build cannot read is skipped, not deleted:
it may belong to a build with another update key or a later format.
`garminctl application recovery list` and `snapshots` name skipped entries and
the `remove-package ID` or `remove-snapshot ID` command that deletes one; see
[application recovery](cli.md#application-recovery). Downloading a release
again replaces a damaged copy of its package.

### Uninstall

Uninstall through
Windows Installed Apps (**garminctl Desktop**), `sudo apt remove garminctl`, or
`sudo dnf remove garminctl`. Uninstall preserves configuration and caches.
On Windows it also removes the sign-in shortcut and the server copy created by
`garminctl autostart on`; a server that is still running keeps its copy until
it exits.

Sources: [installer](../build/windows/installer.nsi),
[Linux packages](../scripts/package-linux.sh),
[desktop lifecycle](../internal/ship/desktop/backend.go),
[launch options](../cmd/garmin-desktop/main.go).
