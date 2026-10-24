# Garmin Connect and automatic sync

Sign in once with `garminctl cloud login` or from the Garmin Connect tab in the
desktop app or web UI. Sign-in follows Garmin Connect Mobile's flow: Garmin
SSO, an OAuth 1.0 token, then a bearer token. Only tokens are stored in
`~/.config/garminctl/garmin-connect.json` on Linux/WSL, or
`%APPDATA%\garminctl\garmin-connect.json` on Windows. The file is created with
mode `0600` on Unix; the password is never written. Windows and WSL use
separate token stores, so changing from a WSL server to the Windows app can
require a new sign-in.

## Manual sync

`garminctl cloud sync` uploads recorded files for which Garmin has an upload
endpoint (activities, daily monitoring, sleep, metrics, HRV, and others) that
this computer has not sent before. It recognizes files already synced by the
phone when Garmin answers "duplicate". It then pulls and acknowledges
Connect's device-message queue, including settings-change files, Connect IQ
app installs and updates queued from the store website or Connect's
auto-update, workouts, courses, golf clubs, and personal records. Files are
written where the watch expects them. Nothing is deleted from the watch.

`cloud settings` shows the device settings record Connect keeps.
`cloud settings set` changes it and fetches the settings file Connect generates
into `GARMIN/NewFiles`. Use this for a `managedByConnect` field so Connect
stops reverting it. Types requiring privacy consent in Connect (HRV, skin
temperature, sleep disruptions, and others) are skipped with a note until
consent is given.

## Automatic USB sync

The desktop application and `garminctl serve` watch USB by default. When the
watch appears, the server connects, syncs with
Connect if signed in (uploads new files and writes queued settings, apps, and
workouts), checks Garmin's update service, and shows a Windows notification
with the outcome. The web UI follows the watch connection and shows the same
events as toasts. Use `-autosync=false` to turn off the watcher.

`-autosync=false` is a `garminctl serve` option. The desktop app can reuse a
server started with that option; its own server enables automatic sync for a
live watch and disables it for a `-root` dump. Closing the native app stops a
server it started and its automatic sync. A reused server keeps running.

Only a Garmin device counts as the watch: manufacturer `Garmin` or USB vendor
`091E`. Phones and USB drives are ignored.

`info`, Overview, Music, Store detail, and Updates show the storage capacity
and free space reported by the watch through PTP GetStorageInfo.
`garminctl autostart on` installs a Windows Startup shortcut that runs the
Windows build of `serve` hidden at sign-in from
`%LOCALAPPDATA%\garminctl`, enabling the watcher, sync, and notifications
without a terminal. `garminctl autostart off` removes it.
This is a separate background-server feature; the desktop installer does not
enable it or automatically open the native window at sign-in.
