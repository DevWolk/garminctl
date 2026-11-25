# garminctl and the alternatives

This page compares garminctl with Garmin Express, Garmin's own desktop
program, and with other open-source tools.

What it says about another program comes from that program's own pages, which
are linked. Nothing here was established by installing or running the other
programs, and the search for open-source tools was not exhaustive. Garmin and
the projects change, so check the links for the current state.

## Garmin Express

Garmin describes Garmin Express as desktop software to
"[update maps and software, sync with Garmin Connect and register your device](https://www.garmin.com/en-US/software/express/windows/)".
It is Garmin's own program, with Garmin's support.

### Which one to use

Use **Garmin Express** when you want Garmin's support behind the program, use
a Mac, have a Garmin device that is not a watch, or need maps, marine charts,
golf courses, product registration or Wi-Fi setup.

Use **garminctl** when you want to change watch settings over USB without
Garmin Connect, work from a terminal or through an AI assistant, or manage the
watch without a Garmin account.

On Linux, also look at [openExpress](#open-source-tools). It covers maps and
courses, which garminctl does not, and garminctl's own Linux USB support is
experimental.

garminctl does not replace or change Garmin Express, so you can keep both
installed. By default Garmin Express
[runs in the background and reacts when a device is connected](https://support.garmin.com/en-US/?faq=1U931Uczj36W9sUBa8c2N6).
Avoid working on the watch with both at the same moment.

### Feature by feature

| | Garmin Express | garminctl |
| --- | --- | --- |
| Made by | Garmin, with Garmin's support | An independent open-source project under the MIT license. Not affiliated with Garmin; use at your own risk. |
| Systems | [Windows 10 or later and macOS. Linux is not supported.](https://support.garmin.com/en-US/?faq=4QVp7mKSIA1LDk5fc1OHX8) | Windows 10 or later, WSL, and Linux with experimental USB. No macOS. |
| Devices | [Garmin devices](https://support.garmin.com/en-US/?faq=IB1TjAKZso2F6mKr3hYqy9); it also updates street maps, marine charts and golf courses | Watches. There is no list of supported models. |
| Watch settings | A Garmin watch manual [places them in the Garmin Connect app or website](https://www8.garmin.com/manuals/webhelp/GUID-5D183A14-BB43-4A9B-B441-5F824214CE40/EN-US/GUID-AA8474F8-9F52-4DBE-AAF2-706B72A2231F.html), or on the watch; a sync then applies them. Garmin's pages describe no editor for general watch settings in Garmin Express. | Reads the settings from the watch and writes changes over USB. Needs no account and no internet access. |
| Connect IQ app settings | [Yes](https://support.garmin.com/en-US/?faq=6Vl7EbW3qA42l63ZIhJet5) | Yes, also from the CLI and MCP. A copy of the settings file is saved first. |
| Connect IQ store apps | [Manages them](https://www.garmin.com/en-US/software/express/windows/), including [removal](https://support.garmin.com/en-US/?faq=ZOJb1R5G9M4PKsnxOyIdv7) | Browses the store, removes apps, hides and shows them. It cannot install a store app by itself: it delivers apps that Garmin Connect has queued, and sideloads a `.PRG` file you provide. |
| Sync with Garmin Connect | [Yes](https://www.garmin.com/en-US/software/express/windows/) | Yes |
| Watch software updates | [Yes](https://support.garmin.com/en-US/?faq=4PgEa32Yc2Av8hZM0EAXc7), with notifications when updates are available | Yes. Checks when the watch is plugged in; installs only when you choose. |
| Workouts | [Sends workouts and training plans](https://www8.garmin.com/manuals/webhelp/GUID-5D183A14-BB43-4A9B-B441-5F824214CE40/EN-US/GUID-9A740EA8-1467-4033-891D-1B98F08876D9.html) from the Garmin Connect website to the watch | Creates a workout from a description, sends Garmin Connect workouts, or writes one straight to the watch without an account. |
| Music | [Yes](https://support.garmin.com/en-US/?faq=1ZDlVH09XB1169yYD5FIWA) | Yes |
| Maps | Yes, including [map management on select watches](https://support.garmin.com/en-US/?faq=T5bIktSUWiAkZKnYOV3Cq6) | No |
| Registration, vouchers, [Wi-Fi networks](https://support.garmin.com/en-US/?faq=86QHylLwpf6iG9UtYr4fY7), [language files](https://support.garmin.com/en-US/?faq=bUNm3O11dH04aqGOFtBsz6) | Yes | No |
| Files on the watch | [Saves backups](https://support.garmin.com/en-US/?faq=RczEqIQ0Sk1IONZXf1sAl9) of a device | Browse, upload, download and delete files; show FIT files in readable form; copy the storage. |
| Activities and health | [Uploads them to Garmin Connect](https://www.garmin.com/en-US/software/express/windows/) | Uploads them, and shows activities with maps and charts from the watch and daily health from Garmin Connect. |
| Automation | A desktop application. Garmin's pages describe no command line or API for it. For your own online data, Garmin offers the [Garmin Connect Personal API](https://developer.garmin.com/gc-developer-program/personal/personal-faq/). | A command line with JSON output, a local HTTP API, and MCP tools for AI assistants. |
| Garmin account | Garmin's steps for adding a device [include signing in](https://support.garmin.com/en-US/?faq=eagCIGaxGr1eDoN2yOW2b8) | Needed only for Garmin Connect features. |
| Background process | [Designed to run as a background service](https://support.garmin.com/en-US/?faq=bfUKCsauQT7sTGUFDRPok8); this can be turned off | Runs while the app or the server is open. Starting with Windows is optional. |

### What garminctl costs you

- **No support from Garmin.** garminctl talks to the same Garmin services as
  Garmin's own apps. Garmin can change them at any time, and a feature can
  stop working until garminctl is updated.
- **No list of supported watches.** garminctl works with the files a watch
  shows over USB. Its tests use sample files, and another model or software
  version can behave differently.
- **Windows does not recognize its publisher.** The Windows files are not
  signed by a publicly trusted authority.
  [Smart App Control](desktop.md#smart-app-control) can block the command-line
  program.
- **Linux USB is experimental** and has not been verified on physical
  hardware.
- **Fewer functions around the watch.** See the "No" rows above.

## Open-source tools

Some of these tools reach the watch; others work on Garmin Connect's online
data.

| Tool | Reaches | What it does, in its own description | Next to garminctl |
| --- | --- | --- | --- |
| [openExpress](https://github.com/TomaszBojanowski/openExpress) | The watch over USB, and Garmin's services | A Linux app for Garmin watches and bike computers, with a window, a command line and background operation: sync with Garmin Connect, software updates, maps, Connect IQ apps and music. It describes itself as beta and as containing no Garmin code; setup reads keys from your own copy of the Garmin Express installer. | The closest in scope, and it covers maps and bike computers, which garminctl does not. It states that Connect IQ app settings cannot be changed, and its feature list has no editor for watch settings. It is for Linux; garminctl also runs on Windows and WSL. |
| [wristconf](https://github.com/luisfelipecr/wristconf) | The watch over Bluetooth | Reads, backs up and changes the settings of a Garmin watch from a computer: activity profiles, data screens, glances, controls, display, alerts. It describes itself as alpha and as tested on one watch model. | It works through the watch's own settings menus over Bluetooth, a different route from garminctl's settings files over USB. It covers settings only. |
| [Gadgetbridge](https://codeberg.org/Freeyourgadget/Gadgetbridge) | The watch over Bluetooth | An Android app that replaces the vendor's phone app. For Garmin it [lists](https://gadgetbridge.org/basics/topics/garmin/) activity sync, settings, workouts and app files among its features, and app management among the missing ones. | A phone app, not a desktop program. |
| [python-garminconnect](https://github.com/cyberjunky/python-garminconnect) | Garmin Connect online | A Python library for Garmin Connect's health, fitness and device data. It can write device settings through the account. | It states that it does not connect directly to a watch. garminctl uses the same kind of sign-in for its Garmin Connect features. |
| MCP servers for Garmin Connect: [garmin_mcp](https://github.com/Taxuspt/garmin_mcp), [garmin-connect-mcp](https://github.com/Nicolasvegam/garmin-connect-mcp) | Garmin Connect online | Give an AI assistant your fitness and health data from Garmin Connect. | These two work through Garmin Connect's online data. garminctl's MCP tools also read and change the watch. |
| [GarminDB](https://github.com/tcgoetz/GarminDB) | Garmin Connect online, or files copied from a mounted watch | Loads health data into a local database for analysis. | A tool for analysis; garminctl has none of its reports. Its README lists nothing that writes to a watch. |
| [Garmin MTP CLI](https://github.com/Likenttt/garmin-mtp-cli) | The watch over USB | A command-line tool for reading and writing the files a Garmin watch shows over MTP. | Works with files as they are. garminctl also reads their contents and builds the changes. |

Two more tools serve other needs: [antfs-cli](https://github.com/Tigge/antfs-cli)
extracts activity files from watches that sync over ANT-FS, and
[GPSBabel](https://www.gpsbabel.org/) converts and transfers waypoints, tracks
and routes for many GPS devices.

### Where garminctl differs

- **Watch settings over USB.** We found no other open-source tool that changes
  them over USB. wristconf and Gadgetbridge do it over Bluetooth, and
  python-garminconnect through the Garmin Connect account.
- **Connect IQ app settings on the watch.** We found no other open-source tool
  that changes them on a computer; openExpress states that it cannot.
- **Windows.** garminctl runs on Windows and WSL as well as Linux.
- **One program for people, scripts and assistants.** The desktop app, the
  browser interface, the command line and the MCP tools use the same code.

If you know a tool that belongs here, or see a statement that is no longer
true, open an issue.
