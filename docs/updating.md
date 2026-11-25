# Updating garminctl

This page is about updating garminctl itself, the program on your computer.
For watch software and watch apps, use the **Updates** tab or
[`garminctl updates`](cli.md#settings-and-apps).

garminctl does not update itself in the background, and it contacts GitHub only
when you ask it to. Your system's own updates do not include it either: the DEB
and RPM packages add no package repository.

## Choose your update

| You installed | Update |
| --- | --- |
| Windows Setup (also used from WSL) | [By hand](#update-by-hand), or [from the app](#update-from-the-app). |
| Ubuntu DEB or Fedora RPM | [By hand](#update-by-hand), or [from the app](#update-from-the-app). |
| Portable CLI archive | [Replace the standalone CLI](#update-a-standalone-cli) in its own directory. |
| CLI installed with Go | [Reinstall from source](#update-a-cli-installed-with-go) with an explicit `GOBIN`. |

On Windows with Smart App Control on, update by hand; see
[Smart App Control](desktop.md#smart-app-control).

## Update by hand

1. Download the newer package for the same system and architecture from
   [GitHub Releases](https://github.com/DevWolk/garminctl/releases/latest).
   In the desktop app, **Help → Application updates…** opens the
   **Application** tab, which links there.
2. Quit garminctl. Stop any separate `garminctl serve` process too.
3. Install the package:

   | Installation | Command |
   | --- | --- |
   | Windows or WSL desktop | Run the new Setup and keep the existing installation directory. |
   | Ubuntu DEB | `sudo apt install ./garminctl_NEW_VERSION_linux_amd64.deb` |
   | Fedora RPM | `sudo dnf upgrade ./garminctl_NEW_VERSION_linux_amd64.rpm` |

   Replace `NEW_VERSION` with the downloaded version.
4. Open garminctl again.

Your [data](desktop.md#where-garminctl-keeps-its-data) stays in place. A server
that was running must restart to use the new binary. MCP configuration can stay
unchanged when its URL or executable path stays the same.

Windows Setup also updates the server copy made by `garminctl autostart on`. A
copy in use by a running server keeps its old version.

## Update from the app

The **Application** tab of the desktop app can download, verify and install a
release. It is not available in a browser: there the tab shows the backend
version and a link to GitHub Releases.

### What the tab shows

- Separate versions of the desktop app, the CLI and the backend (the local
  server), your platform, and the detected installation type. An installation
  that garminctl cannot identify, and versions that differ, stay visible.
- Saved release history, with the time of the last successful check. It stays
  available offline. Release notes are shown as text.
- A Releases link, which opens the browser for a manual download.

Opening the tab does not contact GitHub or start a download.

### Steps

1. Choose **Check for updates**. This refreshes the release history from
   GitHub. Stable releases are selected by default. **Include prereleases**
   changes which saved releases are shown, without another network request.
2. Choose **Download selected release**. garminctl downloads the package,
   checks its signature and saves it on this computer. **Cancel download** is
   complete only when the unfinished download has been removed. Reloading the
   page keeps watching the same download.
3. Choose **Install saved release…** and confirm. garminctl then:
   - verifies the selected package and a saved package of the version you run
     now;
   - saves a protected [backup of your garminctl data](#what-garminctl-keeps-for-updates);
   - hands the package to the system installer.

   You can cancel during the first two steps. Once the handover has started,
   the installation cannot be cancelled.
4. garminctl closes and the installer runs: Windows Setup, or the system
   package manager. On Ubuntu and Fedora you are asked once for system
   authorization (`pkexec`); the installed `garminctl` then verifies the package
   again before the package manager installs it.
5. The installer starts garminctl again. Check the result in **Application**.

> [!IMPORTANT]
> Once the system installer has taken over, garminctl cannot see whether it
> succeeded. Check the result after the restart. An installation that did not
> complete is shown in **Application** on the next start and by
> `garminctl application status`, with the directory of its logs.

If an installation did not complete, the programs may already be replaced.
Check the installed versions in **Application** before you try again. To
repair the installation, [update by hand](#update-by-hand).

### When the app refuses

A missing condition is reported, and nothing is changed.

To download a release:

| Condition | If it is not met |
| --- | --- |
| The build has an update key: the public key that release builds use to verify packages. | The button is disabled. Source builds without the key cannot download or install; update by hand. |
| The desktop app and the CLI are the same build, and garminctl can read both versions. | Install a package by hand; it installs both. |
| garminctl recognizes how it was installed. | Update by hand. |

To install a saved release, also:

| Condition | If it is not met |
| --- | --- |
| The app was installed by Setup, DEB or RPM, on amd64. | Update by hand. |
| The app runs its own server. | The saved packages cannot be listed. If the app uses a `garminctl serve` that you started, stop that server and open the app again. |
| The selected release is newer than the installed one. | To go back, use [return to an older version](#return-to-an-older-version). |
| A package of the version you run now is saved too, as a way back. | Select your current version in the release list and download it as well, or update by hand. |
| No watch job, such as a sync, is running. | Wait for it to end. |
| On Linux: `pkexec` and the package tools are present, and the installed `garminctl` belongs to root. On Windows: the installed `garminctl.exe` is not in use. | The message names what is missing or running. It is shown before the app closes. |

## Return to an older version

You can go back to an older saved version together with a backup of your
garminctl data from that time.

1. In **Application**, under **Return to an older version**, select an older
   saved version and its matching data backup.
2. Decide what happens to your current data:
   - To replace it with the backup, leave the option off.
   - To keep it, select **Keep current data (requires compatibility check)**.
3. Choose **Prepare rollback review…**. garminctl verifies the packages and
   saves a fresh copy of your current data. It does not restore or install
   yet.
4. Review the selected mode, the two backup IDs and every file that will be
   replaced or removed.
5. Check the consent box, then choose **Restore backup and install older
   version** or **Keep current data and install older version**.

**Cancel** keeps the current files and waits for cleanup. After you confirm,
the work cannot be cancelled; a request to close the window waits until it
ends. Reloading resumes the same review and never confirms it for you.

### The two modes

| Mode | What happens to your data |
| --- | --- |
| Restore backup | The listed files are replaced with the selected backup. Newer changes to them remain only in the fresh safety copy. |
| Keep current data | No data is replaced. garminctl checks the current files again after the server stops, before the installer starts. |

Other files, browser data and saved packages stay in place in both modes.

**Keep current data** needs proof that the older version can read today's data.
Both backups must record their data formats, and the formats must match.
garminctl also checks the formats of the current account, verification and
upload-record files. If a format is missing, unknown or incompatible, this mode
is refused. It never switches to restoring the older copy on its own.

### If something goes wrong

Restoring several files is not atomic. A failure can leave part of the data
restored, or an uncertain installer result. The dialog states whether your data
is unchanged, partly restored or fully restored.

After a partial or full restore, do not repeat the operation. garminctl does
not repair a partial restore by itself. Keep both backup IDs and the packages:
the backups stay in `<config>/application-backups/`, and the
[recovery commands](#recovery-commands) list and check them.

## What garminctl keeps for updates

Updates and rollbacks keep the files below, private to the current user.
`<config>` and `<cache>` are the directories in
[where garminctl keeps its data](desktop.md#where-garminctl-keeps-its-data).

| Directory | Contents | Kept |
| --- | --- | --- |
| `<config>/application-backups/` | Data backups, one saved before each installation and each rollback review: copies of the Garmin Connect sign-in and pending-verification files, the upload records and the safety copies of watch files. The CLI calls them snapshots. | The 10 newest. |
| `<cache>/application-packages/` | Downloaded packages with their signatures. | The 5 most recently saved, and always the package of the installed version. |
| `<cache>/application-recovery/` | Copies made by `application recovery export`. | Until you delete them. |
| `<cache>/application-staging/` | Working copies of a download or a verification. | Removed when the operation ends. |
| `<cache>/application-installing/` | The package, a copy of the CLI and the log of an installation. | Removed when an installation is canceled. Once the system installer has taken over, the files of the installation with the newest recorded result are kept. |
| `<cache>/application-history/` | The release list of the last successful check. | Replaced by the next successful check. |

These numbers cannot be changed.

- **Backups.** Once the installer accepts an update, the backups beyond the
  newest 10 are deleted. An update that is refused or canceled, and a rollback,
  delete none. A retry therefore does not push out older backups, and the
  backup selected for a rollback stays.
- **Packages.** A download that saves a new package deletes the packages beyond
  the limit. Of two saved copies of one release, the later one is kept.
- **Leftovers.** A working copy left by an interrupted operation is deleted by
  the next download, update or installation once it has been unchanged for a
  day.
- **Sign-out.** Signing out of Garmin Connect, in the app or with
  `garminctl cloud logout`, deletes the token and pending-verification copies
  from every backup. The backups stay valid for their other files.
- **Unreadable entries.** A saved package or backup that this build cannot read
  is skipped, not deleted: it may belong to a build with another update key or
  a later format. The [recovery commands](#recovery-commands) name such entries
  and can delete them. Downloading a release again replaces a damaged copy of
  its package.

## Recovery commands

These commands work without the desktop app, a running server or a watch. Use
them to see what is saved and to get a verified package for a manual repair.
None of them restores data or runs an installer.

```text
application status                       Show the result of the last update installed from the app.
application recovery list                List saved packages for this OS and architecture.
application recovery export ID           Verify one saved package and copy it out.
application recovery remove-package ID   Delete one saved package.
application recovery snapshots           List data backups.
application recovery inspect ID          Check a data backup's files without restoring them.
application recovery remove-snapshot ID  Delete one data backup.
```

### Status

`status` reads the installer's own record: installed, failed with its reason,
or still running when monitoring ended, plus the directory of its logs. With
`-json` it returns `lastInstallation`, or `null` when nothing was recorded.

### Saved packages

`list` shows IDs to use with `export` and `remove-package`. The list is
metadata, not proof of authenticity.

`export ID` verifies the package offline with the update key built into this
CLI. It keeps a separate copy of the original package and its signature under
`<cache>/application-recovery/`, and prints both paths. It starts no installer
and stops no server.

- A missing key, an unknown ID, a damaged package or an invalid signature
  fails without replacing any installed file.
- Source installs without an embedded update key can list and remove packages
  but cannot export them.
- No command argument or environment variable can choose another key.

To repair an installation by hand:

1. Export the package.
2. Stop the application and its server.
3. Install the exported file with the OS installer or package manager.
   Package-managed files must stay managed by that manager.

Choosing an older package does not establish that it can read your current
data. Compatible rollback and restoring older data are done in the
[rollback review](#return-to-an-older-version). Keep the exported files until
the repair is finished; nothing removes them automatically.

### Data backups

`snapshots` and `inspect` read `<config>/application-backups/`. They work
without the update key. Use an ID from `snapshots`; a path cannot be given. An
ID that is not kept is reported as not retained.

`inspect ID` checks the backup's manifest, file names, sizes and SHA-256
digests, rejects unexpected or missing files and enforces private permissions
on the backup. It shows relative file names and hashes, never token contents.
It does not read or replace your current account files.

A verified backup means that its files match its own manifest. It is not a
release-signature check and not a compatibility decision.

### Unreadable entries

`list` and `snapshots` show the entries this build can read. For each skipped
entry they print a warning with its ID, the reason, and the `remove-package ID`
or `remove-snapshot ID` command that deletes it. Both removal commands delete
exactly the named entry, readable or not, and change no installed file and no
current data.

### JSON output

| Command | `-json` output |
| --- | --- |
| `list` | `verified: false`, a `packages` array and an `unusable` array of skipped `id`/`reason` pairs. |
| `snapshots` | The backups and the same `unusable` array. |
| `remove-package`, `remove-snapshot` | `removed` with the ID. |
| `export` | `verified: true`, the package metadata, `directory`, `path`, `signaturePath`, `manualInstallationRequired: true` and `dataCompatibility: "not_checked"`. A failed verification returns no success JSON. |
| `inspect` | `verified: true`, `verification: "local_manifest"` and `dataCompatibility: "not_checked"`. |

## Update a standalone CLI

A portable archive contains only the CLI, in one folder named after the
archive. Keep this copy in its own directory, apart from Windows Setup's
directory and `/usr/bin`. Do not copy a portable CLI over the binary that Setup
or a package installed.

Before you start:

- Choose the archive for your OS and architecture and verify it against
  `checksums.txt`.
- Unpack it into a temporary directory.
- Stop servers and stdio MCP clients that were started from the copy you are
  replacing. A running process keeps using its old code until it restarts.

### Linux and WSL

With the download unpacked into `./unpacked`, stage the new executable and
check it first. Set the directory to where your copy lives; the example uses a
dedicated user location:

```sh
garminctl_cli_dir="$HOME/.local/share/garminctl-cli/bin"
mkdir -p "$garminctl_cli_dir"
install -m 0755 ./unpacked/garminctl_*/garminctl "$garminctl_cli_dir/garminctl.new"
"$garminctl_cli_dir/garminctl.new" version
```

After checking the version, keep the existing executable if there is one, then
replace only this copy:

```sh
if [ -f "$garminctl_cli_dir/garminctl" ]; then
    cp -p "$garminctl_cli_dir/garminctl" "$garminctl_cli_dir/garminctl.previous"
fi
mv -f "$garminctl_cli_dir/garminctl.new" "$garminctl_cli_dir/garminctl"
"$garminctl_cli_dir/garminctl" version
```

### Windows

1. Stop every process that uses the standalone directory.
2. Keep the previous `garminctl.exe` beside the new one.
3. Replace `garminctl.exe`, then check the new executable by its full path.
4. Restart only the server or client that uses this copy.

Replacing the executable on native Windows, including how locked files behave,
has not been verified.

### Update a CLI installed with Go

Select a published version and a dedicated `GOBIN`. The example pins the source
version and the version the CLI displays; replace `vX.Y.Z` with the tag:

```sh
garminctl_version=vX.Y.Z
garminctl_go_bin="$HOME/.local/share/garminctl-source/bin"
mkdir -p "$garminctl_go_bin"
GOBIN="$garminctl_go_bin" CGO_ENABLED=0 go install \
    -ldflags "-X main.version=$garminctl_version" \
    "github.com/DevWolk/garminctl/cmd/garminctl@$garminctl_version"
"$garminctl_go_bin/garminctl" version
go version -m "$garminctl_go_bin/garminctl"
```

- `go install` writes to `GOBIN`, not to the first `garminctl` on `PATH`; see
  the [Go command reference](https://go.dev/ref/mod#go-install).
- Without the version flag, a source-built CLI displays `dev`.
  `go version -m` shows its module build information.
- A source build has no desktop app and no update key. Use a package to update
  the desktop app with its CLI, or to get native Linux USB support.
- Keep the previous executable if you want a way back.

### After replacing a CLI

Replacing an executable does not restore configuration. Going back to an older
executable also needs data that it can read: keep your current data, and use
the [rollback review](#return-to-an-older-version) when you need an older data
set. Keep the executable path in your MCP configuration unchanged, or update
the configuration when you choose a new path.

Sources: [update policy](../internal/containers/application),
[download, verification and installation](../internal/ship/updatetransport),
[recovery commands](../internal/containers/application/ui/cli).
