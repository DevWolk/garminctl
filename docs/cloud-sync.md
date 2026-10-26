# Garmin Connect and sync

Sign in from **Garmin Connect** in the app or web UI, or use the CLI:

```sh
garminctl cloud login
garminctl cloud status
garminctl cloud sync
```

Login prompts for credentials and a verification code when required.
`cloud login -email ADDRESS`, `GARMIN_EMAIL`, and `GARMIN_PASSWORD` also supply
credentials. Complete verification in the login prompt; `cloud logout` removes
the saved tokens.

The app stores tokens in an owner-only directory (`0700` on Unix), with
owner-only files (`0600` on Unix and a protected current-user DACL on Windows).
It does not save the password. Saving replaces a complete temporary file;
a failed write or replacement keeps the previous tokens. Config-path,
protection and unreadable-session errors are reported instead of silently
using another directory. `cloud logout` can remove a malformed token file.
See [data locations](desktop.md#data-updates-and-uninstall) for token paths and
the separate Windows/WSL stores.

## Manual sync

`cloud sync` uploads supported recordings this computer has not sent, then
downloads and acknowledges Connect's queued files. Uploaded recordings remain
on the watch. Garmin's duplicate responses are recorded to avoid repeat uploads.

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

Sources: [sign-in and token storage](../internal/gc/client.go),
[sync](../internal/garmin/cloud.go), [USB watcher](../internal/web/auto.go).
