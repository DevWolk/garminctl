# Garmin Connect and sync

Garmin Connect is optional. Without it, everything that needs only the watch
still works. Sign in to:

- upload recordings to Garmin Connect and receive the files it has queued for
  the watch;
- see daily health data, records, badges and weight;
- save workouts in Garmin Connect and send them to the watch from there (a
  workout can also be written straight to the watch without sign-in);
- change the settings that Connect manages.

## Sign in

In the app or web UI, open **Garmin Connect**, enter your Garmin email address
and password, and choose **Sign in**. If Garmin sends a verification code, the
tab asks for it.

In a terminal:

```sh
garminctl cloud login
garminctl cloud status
```

`cloud login` asks for your email address and password and, when Garmin
requires one, a verification code.

garminctl never saves the password, in the app or in the terminal. It saves
the tokens that Garmin returns; see
[where the sign-in is stored](#where-the-sign-in-is-stored).

| Way to give credentials | How |
| --- | --- |
| Prompt | `garminctl cloud login` |
| Email as a flag | `garminctl cloud login -email ADDRESS` |
| Environment | `GARMIN_EMAIL` and `GARMIN_PASSWORD` |

If the input is not a terminal, or you leave the code prompt empty, finish
later with `garminctl cloud mfa CODE`.

### Verification codes

- A pending verification is saved beside the sign-in file as
  `garmin-connect.json.mfa`, with the same file protection.
- It is kept for ten minutes. This limit is garminctl's own; Garmin may expire
  a code sooner.
- A wrong code keeps the pending verification for another attempt.
- Starting a new sign-in replaces it. A completed sign-in and `cloud logout`
  remove it. An expired one is removed when you try to use it.
- `cloud mfa CODE` sends the code for the newest sign-in attempt. garminctl
  cannot tell whether a code came from an earlier attempt.
- If Garmin accepts a code but the sign-in still fails afterwards, run
  `cloud login` again. Your previous sign-in file is kept.

### After signing in or out in a terminal

A running app or server keeps the account it has loaded and does not notice the
change. Sign in or out in its own **Garmin Connect** tab, or restart it.

### Where the sign-in is stored

The sign-in is a token file; see
[where garminctl keeps its data](desktop.md#where-garminctl-keeps-its-data) for
its path. Windows and WSL have separate files.

- Only your user account can read the directory and the file (`0700` and
  `0600` on Unix, a protected access list on Windows).
- Saving replaces the file with a complete new one. A failed save keeps the
  previous sign-in.
- If the configuration directory cannot be used, or the saved sign-in cannot
  be read, garminctl reports the error. It does not fall back to another
  directory.
- `cloud logout` can remove a damaged sign-in file.
- Signing out, from the CLI or the app, also deletes the copies of the sign-in
  and pending-verification files that
  [application data backups](updating.md#what-garminctl-keeps-for-updates)
  hold. Those backups stay usable for their other files.

## Manual sync

Choose **Sync now** in the **Garmin Connect** tab, or run:

```sh
garminctl cloud sync
```

A sync has two directions:

| Direction | What happens | Skip it with |
| --- | --- | --- |
| Upload | Recordings that this computer has not sent yet go to Garmin Connect. They stay on the watch. | `-no-upload` |
| Download | Files that Connect has queued for the watch are written to it and acknowledged. | `-no-download` |

- Queued files can be settings, Connect IQ apps and updates, workouts, courses
  and personal records. What is available depends on the watch and on
  Connect's queue. `garminctl cloud messages` lists the queue.
- Unplug the watch after a sync so that it applies the downloaded files.
- If the watch is unplugged during a sync, the sync stops and reports what it
  had done. Sync again later: accepted uploads are remembered, and files not
  yet written are still in Connect's queue.
- When Garmin answers that it already has a recording, garminctl remembers
  that and does not upload the recording again.
- Some recording types need privacy consent in Garmin Connect. They are
  skipped until you give it.
- `cloud sync` exits with an error when an upload failed, a queued file was not
  written to the watch, or a direction could not run. With `-json` it prints
  the result first.

### If the upload record is damaged

garminctl records what it has uploaded in a file on this computer.

- If that file is damaged or cannot be read, uploads stop and an error is
  reported.
- If Garmin accepts files but saving the record fails, the sync result lists
  the accepted files and reports the save error. Fix the storage problem
  before you retry; garminctl does not upload those files again by itself.

### Settings that Connect manages

Garmin's service keeps its own copy of some watch settings. In **Watch
settings** these fields are marked `managedByConnect`. If you change such a
field only on the watch, Connect's value returns with a later sync. Change it
in Connect as well, in either way:

- In the **Garmin Connect** tab, edit the setting, choose **Review and apply**
  and confirm.
- In a terminal:

  ```sh
  garminctl cloud settings
  garminctl cloud settings set KEY=VALUE
  ```

  `cloud settings` prints the keys. `cloud settings set` saves the values in
  Connect and fetches the settings file that Connect generates for the watch.

Unplug the watch afterwards so that it applies the file.

## Automatic USB sync

The desktop app and `garminctl serve` watch for a watch on USB. When one
appears, the server:

1. connects to it;
2. if you are signed in, uploads recordings and downloads queued files;
3. checks for watch software updates. It does not install them.

Events appear in the UI and as Windows notifications.

### Turn automatic sync off

The app has no switch for it. There are two ways:

- **Stay signed out of Garmin Connect.** The app still connects to the watch
  and checks for watch software updates, but it does not sync.
- **Run the server yourself without it.** Start the server in a terminal and
  leave the terminal open:

  ```sh
  garminctl serve -autosync=false
  ```

  Then open the desktop app, or `http://127.0.0.1:8787` in a browser. The app
  finds this server and uses it.

  With this option:

  - The server no longer detects the watch. Choose **Connect watch** after
    plugging it in.
  - The server keeps running when the app's window closes. Stop it with
    Ctrl+C in the terminal.
  - The **Application** tab cannot install garminctl updates while the app
    uses a server that you started;
    [update by hand](updating.md#update-by-hand).

On Windows, see [run the CLI on Windows](desktop.md#run-the-cli-on-windows) for
how to type the command. Reading a directory (`-root`) also turns automatic
sync off.

### Start the server when you log in to Windows

`garminctl autostart on` makes a server start, hidden, when you log in to
Windows. It copies the CLI to `%LOCALAPPDATA%\garminctl\garminctl.exe` and adds
a shortcut to the Windows Startup folder that runs `garminctl serve` from that
copy. `garminctl autostart off` removes the shortcut, and
`garminctl autostart status` shows the current state.

- It starts the server only, not the desktop window.
- From WSL, it needs a Windows `garminctl.exe` beside the WSL binary.

Sources: [sign-in and token storage](../internal/ship/gc/client.go),
[sync](../internal/containers/connect/cloud.go), [USB watcher](../internal/ship/web/auto.go).
