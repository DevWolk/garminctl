# Glossary

Terms used in the garminctl guides. The internal terms of the code are in
[how garminctl works](architecture.md#terms-used-on-this-page).

## The watch

| Term | Meaning |
| --- | --- |
| Activity | A recording made by the watch, such as a run. Stored on the watch as a FIT file. |
| App registry | The watch's list of installed apps and whether each one is shown. Stored in `GARMIN/Apps/OUT.BIN`; a pending change is in `IN.BIN`. |
| Connect IQ | Garmin's platform for watch apps, watch faces, glances and data fields, and the store they come from. |
| Firmware | The watch's main software. One of the parts that the **Updates** tab can install. |
| FIT | Garmin's binary file format for settings, activities and workouts. |
| Glance | A small summary screen of an app in the watch's scrolling list. |
| MTP | Media Transfer Protocol: how a computer reads and writes the watch's storage over USB. |
| `NewFiles` | `GARMIN/NewFiles`, the folder where the watch looks for files to apply when it is unplugged. |
| Part number | Garmin's identifier of a software or data component. `updates install` takes one. |
| Pending change | A change that is written to the watch but that the watch has not applied yet. |
| `.PRG` | The file of a Connect IQ app. |
| `.SET` | The file that holds one Connect IQ app's settings. |
| Sideload | Install a Connect IQ app from a `.PRG` file instead of from the store. |
| Unit ID | The number that identifies one watch. |
| Unplug | End the USB connection. The watch applies pending changes at that moment. |
| Watch face | The screen that shows the time. A Connect IQ watch face is an app with its own settings. |

## Garmin Connect

| Term | Meaning |
| --- | --- |
| Garmin Connect | Garmin's online service and account. Called "Connect" where the context is clear. The **Garmin Connect** tab is garminctl's view of it. |
| `managedByConnect` | A mark on watch settings that Garmin Connect also stores. Connect sends its own value again on a later sync. |
| Queue | The files that Garmin Connect holds for the watch until a sync delivers them. |
| Sign-in | The saved Garmin Connect session of this computer's user. garminctl stores tokens, not the password. |
| Sync | Upload recordings to Garmin Connect and download the files it has queued for the watch. |
| Token | A secret that Garmin issues after sign-in. It proves the sign-in without the password. |
| Upload record | The local list of recordings that this computer has already sent. |
| Verification code | The one-time code that Garmin sends when a sign-in needs a second step. The CLI command is `cloud mfa`. |

## The program

| Term | Meaning |
| --- | --- |
| Backend | The name the **Application** tab uses for the local server. |
| CLI | Command-line interface: the `garminctl` command in a terminal. |
| Data backup, snapshot | A copy of garminctl's own data, saved before an update or rollback. The CLI calls it a snapshot. |
| Directory copy, dump | A folder on the computer that contains the watch's `GARMIN/` tree. Used with `-root DIR` for offline work. |
| Local server | The web server inside garminctl. The desktop window, a browser and an AI assistant all talk to it. |
| Loopback address | An address, such as `127.0.0.1`, that only programs on the same computer can reach. |
| MCP | Model Context Protocol: the way an AI assistant calls garminctl's tools. |
| Package | The file that installs garminctl: Windows Setup, a DEB or an RPM. |
| `PATH` | The list of directories where a terminal looks for a command. |
| Prerelease | A release published for testing before the stable one. |
| Safety copy | A copy of a watch file that garminctl saves on the computer before it changes the file. |
| Stdio | Standard input and output: how an assistant talks to a program that it starts itself. |
| Terminal | A window for typing commands: PowerShell on Windows, a shell on Linux. |
| Update key | The public key built into release builds. garminctl uses it to check that a downloaded package is genuine. |
| WPD | Windows Portable Devices: the part of Windows that garminctl uses to reach the watch. |
| WSL | Windows Subsystem for Linux. |
