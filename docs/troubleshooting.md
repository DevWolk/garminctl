# Troubleshooting

Find the message or the symptom below. Messages are quoted as garminctl prints
them.

Start with these two commands. They show whether the computer sees the watch
and which build you are running:

```sh
garminctl devices
garminctl version
```

After Windows Setup, `garminctl` is not found until you follow
[run the CLI on Windows](desktop.md#run-the-cli-on-windows).

## The watch is not found

| Message or symptom | Cause | What to do |
| --- | --- | --- |
| `no portable device connected; plug the watch in over USB` | Windows sees no portable device. | Connect the watch by USB and try again. |
| `no Garmin device among N portable device(s): ...` | Windows sees other devices, but none that identifies as a Garmin. | Run `garminctl devices` and pass the watch's ID: `garminctl -device ID info`. |
| `device "ID" not found among N portable devices` | The ID given with `-device` matches no connected device. The ID must match exactly. | Copy the ID from `garminctl devices`. |
| `native USB needs the Linux package built with libusb` | This CLI has no USB support of its own and found no Windows PowerShell: the portable CLI on Linux, or on WSL when the Windows drive is not mounted at `/mnt/c`. | On Linux, use the CLI from the DEB or RPM package. On WSL, mount the Windows drive at `/mnt/c`. Or read a directory with `-root DIR`. |
| `powershell.exe not found; the MTP bridge needs Windows or WSL` | A command that needs the Windows bridge, such as `hidden` or `mtp`, ran where Windows PowerShell is not available. | Run these commands on Windows or WSL. |
| `open Garmin USB (install the udev rule, then reconnect the watch)` | Linux did not give access to the watch. | Install the DEB or RPM package, which adds the access rule, then reconnect the watch. |
| `device has no MTP interface; select MTP mode on the watch` | The watch is connected in another USB mode. | Select MTP mode on the watch. |
| `claim Garmin MTP interface (close other watch/file-manager connections)` | Another program holds the watch. | Close file managers and other programs that use the watch. |
| The UI shows **Not connected** although the watch is plugged in. | The server has not opened it yet. | Choose **Connect watch** at the bottom of the sidebar. If that fails, the message is one of the rows above. With `-autosync=false` or `-root`, the server does not detect a watch by itself. |

Native Linux USB is experimental and has not been verified on physical
hardware; see [USB access](desktop.md#usb-access).

## A change did not appear on the watch

| Symptom | Cause | What to do |
| --- | --- | --- |
| You edited a value, but nothing changed. | The edit was not saved. Unplugging drops unsaved edits. | Choose **Review and apply** and confirm before you unplug. |
| A watch setting, a hidden app or a new workout is unchanged. | The watch applies these when the USB connection ends. | Unplug the watch, then check. |
| A setting returns to its old value after a sync. | Garmin Connect keeps its own copy of the field (`managedByConnect`). | Change it in Connect too: [settings that Connect manages](cloud-sync.md#settings-that-connect-manages). |
| You used `-root DIR`. | A directory only receives the pending file; no watch applies it. | Repeat the change with the watch connected. |
| `music add` refuses a file. | A file with the same target is already on the watch. | Remove it with `music rm`, then add it again. |

[When changes take effect](cli.md#when-changes-take-effect) lists every case.

## The desktop app does not start or update

| Message or symptom | Cause | What to do |
| --- | --- | --- |
| `... is occupied by another program or an older garminctl; stop it before opening the desktop app` | Another program, or a garminctl that this app cannot use, holds the address. | Stop that program, or start the app on another port: `garmin-desktop -addr 127.0.0.1:8791`. |
| `desktop server address must be a loopback IP and port` | `-addr` is not a loopback address. | Use an address such as `127.0.0.1:8791`. |
| New launch options have no effect. | The app was already open. | Quit the app, then start it with the options. |
| `Could not load the desktop page. Restart the garminctl backend and reopen the desktop app.` | The server that the window uses is not answering. | Start the server again if you ran it yourself, then reopen the app. |
| On Windows the CLI does not start, or the **Application** tab reports a blocked helper. | Smart App Control blocks `garminctl.exe`. | See [Smart App Control](desktop.md#smart-app-control). |
| **Application** will not download or install a release. | One of the conditions is not met. | See [when the app refuses](updating.md#when-the-app-refuses). |
| An update did not complete. | The system installer failed or was interrupted, or the authorization was not given. | **Application** shows the result on the next start; `garminctl application status` prints it with the log directory. Then see [update from the app](updating.md#update-from-the-app). |

## Garmin Connect

| Message or symptom | Cause | What to do |
| --- | --- | --- |
| `not signed in to Garmin Connect` | No saved sign-in for this user. | Sign in: [Garmin Connect](cloud-sync.md#sign-in). |
| You signed in with the CLI, but the app says you are signed out. | A running server keeps the account it has loaded. Windows and WSL also keep separate sign-ins. | Sign in in the app's **Garmin Connect** tab, or restart the server. |
| A verification code is refused. | The code is wrong, or the pending verification expired after ten minutes. | Try the code again, or start with `garminctl cloud login`. |
| Sign-in is refused with HTTP 401 although the password is correct. | Garmin may have changed the key its own app signs in with. | Check for a newer garminctl release. |
| `a sync is already running` | A manual or automatic sync has not finished. | Wait for it; the **Garmin Connect** tab shows its progress. |
| A sync stopped when the watch was unplugged. | A sync does not continue on a new connection. | Plug the watch in and sync again. Accepted uploads are remembered, and files not yet written are still in Connect's queue. |
| Sync reports an upload record error. | The local record of uploaded files is damaged or cannot be saved. | See [if the upload record is damaged](cloud-sync.md#if-the-upload-record-is-damaged). |
| A recording type is never uploaded. | It needs privacy consent in Garmin Connect. | Give the consent in Garmin Connect. |

## The web UI and the server

| Message or symptom | Cause | What to do |
| --- | --- | --- |
| `forbidden: unexpected Host` | The address in the browser uses a host name other than `localhost`, or another port. | Open `http://127.0.0.1:8787`, or the address the server printed. |
| `forbidden: cross-origin request` or `forbidden: cross-site request` | Another website tried to call the server. | Nothing: the request was blocked. Open the UI from its own address. |
| `garminctl serve` cannot listen on its address (`address already in use`; on Windows, `Only one usage of each socket address`). | A server or another program already uses the port. | Use the running server, stop it, or choose another port with `-addr`. |
| The installed browser app shows an error page. | The server is not running. | Start the desktop app or `garminctl serve`. |
| `application maintenance is in progress` or `application has active operations or jobs` | An application update and other work exclude each other. | Wait until the other work ends. |

## An AI assistant cannot use garminctl

| Symptom | Cause | What to do |
| --- | --- | --- |
| The assistant cannot reach `http://127.0.0.1:8787/mcp`. | The server is not running, or the address differs. | Open the app or run `garminctl serve`, and copy the address from **AI & MCP**. |
| A client in WSL cannot reach the app on Windows. | WSL has no access to Windows's localhost. | Turn on WSL's mirrored networking mode, or reach Windows's localhost from WSL in another way. |
| A stdio client cannot start `garminctl`. | The client does not inherit your `PATH`. | Use the absolute path of the executable. |
| Calls do not appear in **AI & MCP**. | The assistant uses the stdio server, which is a separate process. | Use the HTTP connection to see calls in the tab. |
| Health or workout tools fail. | They need Garmin Connect. | Sign in through the app or the CLI; MCP cannot sign in. |

## Where to look next

- `garminctl serve` writes its log to the terminal it runs in, with one line
  for each API call.
- `garminctl application status` names the directory of the installer's logs.
- `garminctl -h` lists every command.

## Report a problem

Open an issue at
[github.com/DevWolk/garminctl/issues](https://github.com/DevWolk/garminctl/issues)
with the output of `garminctl version`, your operating system, what you did and
the exact message.

Before you post output, remove what identifies you: the unit ID that
`garminctl info` prints, email addresses, and the contents of the sign-in file.
Never attach `garmin-connect.json`.
