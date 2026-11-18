# Garmin Connect and sync

Sign in from **Garmin Connect** in the app or web UI, or use the CLI:

```sh
garminctl cloud login
garminctl cloud status
garminctl cloud sync
```

Login prompts for credentials and, on a terminal, a verification code when required.
`cloud login -email ADDRESS`, `GARMIN_EMAIL`, and `GARMIN_PASSWORD` also supply
credentials. With noninteractive input or an empty verification prompt, finish
later using `garminctl cloud mfa CODE`.

Pending verification is stored beside the token file as `garmin-connect.json.mfa`
with the same file protection. Its ten-minute lifetime is a local policy;
Garmin may expire a challenge sooner. A wrong code retains the pending challenge
and updated cookies for another attempt. Starting a new login replaces it;
successful completion and `cloud logout` remove it. An expired challenge is
removed when resumed. If Garmin accepts a code but OAuth or token saving fails,
start `cloud login` again; the previous token file is preserved.

Use one authentication writer per store. Running servers keep their loaded
account snapshot: use the server's account controls or restart it after a
standalone CLI login/logout. The CLI submits a code against the currently saved
challenge; it cannot identify a code from an older login locally.

The app stores tokens in an owner-only directory (`0700` on Unix), with
owner-only files (`0600` on Unix and a protected current-user DACL on Windows).
It does not save the password. Saving replaces a complete temporary file;
a failed write or replacement keeps the previous tokens. Config-path,
protection and unreadable-session errors are reported instead of silently
using another directory. `cloud logout` can remove a malformed token file.
Signing out, from the CLI or the app, also deletes the copies of the token and
pending-verification files that application data backups hold; those backups
stay usable for the remaining files.
See [data locations](desktop.md#data-updates-and-uninstall) for token paths and
the separate Windows/WSL stores.

## Manual sync

`cloud sync` uploads supported recordings this computer has not sent, then
downloads and acknowledges Connect's queued files. Uploaded recordings remain
on the watch. Garmin's duplicate responses are recorded to avoid repeat uploads.

A damaged or unreadable local upload ledger stops uploads and reports an error.
If Garmin accepts files but saving that ledger fails, the sync result retains
the accepted files and reports the save error. Fix the local storage problem
before retrying; garminctl does not automatically replay that upload.

Queued files can include settings, Connect IQ apps and updates, workouts,
courses, and personal records. Availability depends on the device and Connect's
queue. `cloud messages` lists that queue. Use `cloud sync -no-upload` or
`cloud sync -no-download` to run only one direction.

`cloud settings` reads Connect's device settings; `cloud settings set K=V`
updates them and fetches the settings file Connect generates. Use these for
fields marked `managedByConnect` in Watch settings. Some recording types require
privacy consent in Garmin Connect and are skipped until consent is available.

## Automatic USB sync

The desktop app and `garminctl serve` watch USB by default. When a watch appears,
the server connects, uploads recordings and downloads queued files if signed
in, then checks for software updates. Events appear in the UI and Windows
notifications. Update checks do not install firmware automatically.

`garminctl serve -autosync=false` disables USB watching and automatic sync.
Directory mode (`-root`) also disables the watcher. The desktop can reuse a
server with those options; see [server lifecycle](desktop.md#window-and-server).

`garminctl autostart on` installs a Windows sign-in shortcut for a hidden server.
From WSL it requires a Windows CLI binary beside the WSL binary. It does not
open the desktop window; see [CLI server commands](cli.md#servers).

Sources: [sign-in and token storage](../internal/ship/gc/client.go),
[sync](../internal/containers/connect/cloud.go), [USB watcher](../internal/ship/web/auto.go).
