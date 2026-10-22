# What the watch speaks: formats and transport

Findings from reverse engineering a vívoactive 5 (firmware 17.05) and the
Garmin Express 7.x install on 2026-10-21. Everything here is implemented in
`internal/` and backed by samples in the `testdata/` directories.

## Transport: MTP through Windows Portable Devices

The watch enumerates as USB `VID_091E PID_514A`, an MTP "Portable Device",
not a drive letter. Garmin Express talks to it through the WPD COM API
(`Interop.PortableDeviceApiLib.dll`). garminctl does the same: a PowerShell
process compiles a small C# helper (`internal/device/wpd/wpd.ps1`) that calls
`IPortableDeviceManager`, `IPortableDeviceContent`, `IPortableDeviceProperties`
and `IPortableDeviceResources`; Go talks to it over JSON lines on stdin and
stdout. It works from WSL (`powershell.exe` interop) and from a native
Windows build. Operations verified on the real watch: list with sizes and
dates, read, create, overwrite (delete + create), delete. No dialogs, no
driver changes, no extra installs.

Explorer's `Shell.Application` COM object also reaches the device (that is how
the first exploration was done) but it hides file extensions, reports size 0,
copies asynchronously and shows confirmation dialogs on delete.

Paths are relative to the single storage (`Internal Storage`). Top level:
`GARMIN/`, `Music/`, `Audiobooks/`, `Podcasts/`.

## Files on the watch

`GARMIN/GarminDevice.xml` lists every data type with its folder and transfer
direction (also embedded in `C:\ProgramData\Garmin\CoreService\devices_list.xml`).
The `FIT_TYPE_<n>` names are the `file_id.type` values.

| Purpose | Path | Notes |
|---|---|---|
| Device capabilities | `GARMIN/device.fit` | software parts, file and message capabilities |
| Settings | `GARMIN/Settings/Settings.fit` | FIT type 2, read-only for the host |
| Settings changes (input) | `GARMIN/NewFiles/*.FIT` | any FIT dropped here is consumed by the watch |
| Sport profiles | `GARMIN/Sports/*.fit` | FIT type 3, one per activity |
| Connect IQ app settings | `GARMIN/Apps/SETTINGS/<file>.SET` | Connect IQ serialization, read-write |
| Connect IQ app data | `GARMIN/Apps/DATA/<file>.{DAT,IDX,STR,IMT}` | object store |
| Installed apps | `GARMIN/Apps/OUT.BIN` | protobuf written by the watch |
| Apps config from host | `GARMIN/Apps/IN.BIN` | protobuf written by Express |
| Activities | `GARMIN/Activity/*.fit` | |
| Workouts, Goals, Totals, Records, Sched, Location | `GARMIN/<dir>/*.fit` | |

`.PRG` application binaries are not visible over MTP on this watch.

## Settings.fit

Standard FIT (header 14 bytes, little-endian, profile 21.184). Messages:
`file_id`, `file_creator`, `device_settings` (96 fields, most undocumented),
`user_profile`, `hrm_profile`, `bike_profile`, `connectivity`, `ohr_settings`,
`zones_target` x5, `hr_zone` x24, and undocumented messages 139, 147 (20
sensor slots), 222 (20 alarm slots), 242, 266, 296, 373, 379 (sleep schedule
per weekday), 392, 415, 423. `internal/garmin/knowledge/device_settings.json`
holds what has been inferred about the undocumented ones.

The FIT codec (`internal/fit`) keeps raw bytes per field so unknown data
round-trips exactly; the profile layer names what the public FIT SDK
(21.218) documents.

## Writing settings: the settings-change file

Garmin Connect never rewrites Settings.fit. It downloads
`devicesettings-service/settings-change/fit/<unitId>` and Express copies that
file into `GARMIN/NewFiles/<uuid>.FIT`. Layout (big-endian, profile 21.217):

- `file_id`: type 41, manufacturer 1, product 65534 (`connect`), time_created,
  serial_number = unit id, number 1
- `file_creator`: hardware_version 0, software_version = watch firmware
- repeated message **217**, one per changed field:
  - 0 `uint16` target message number (2 = device_settings, 3 = user_profile, 8 = hr_zone ...)
  - 1 `uint16` message index (optional)
  - 2 `uint8` field number
  - 3 `uint8` array element index (optional)
  - 4 `uint32` new value, unscaled integer
  - 253 timestamp
  - 6, 8, 9, 11 appear for zones, training settings and alarms (meaning unknown)

Garmin uses local message slot 0 only and re-emits a definition whenever the
field layout changes; `fit.Builder` reproduces its file byte for byte
(`TestBuilderMatchesGarminLayout`). `garmin.BuildChangeFile` writes the same
structure. Files stay in `NewFiles` while the USB session is open; the watch
processes them when the session ends (Express's own pending files behaved the
same way).

## Connect IQ app settings (.SET)

Big-endian. Optional string block (`AB CD AB CD`, uint32 length, entries of
uint16 length + UTF-8 + NUL) then a data block (`DA 7A DA 7A`, uint32 length,
one typed value). Type codes: 0x00 null, 0x01 int32, 0x02 float32, 0x03
string (uint32 offset into the string block), 0x05 array (uint32 count),
0x09 bool (1 byte), 0x0B dictionary (uint32 count, key/value pairs), 0x0E
int64, 0x0F float64. The Easy+ watch face stores 88 keys. `internal/ciq`
round-trips the real files byte for byte. The watch face picks the new values
up on its own after the file is rewritten.

## Connect IQ .PRG

Sections of `uint32 magic, uint32 length, bytes`. Seen: `D000D00D` header,
`6060C0DE` entry points, `DA7ABABE` data, `C0DEBABE` code, `C0DE7AB1` link
table, `C1A557B1` class defs, `F00D600D` resources (string table entries are
`00 01 <uint16 len> <text>`), `6000DB01` permissions, `0ECE7105` exceptions,
`5E771465` SETTINGS = default property values in .SET format, `E1C0DE12`
developer signature, `00005161` store signature. The settings *UI definition*
(labels, dropdown options and their values) is not in the PRG; the store
serves it to Express and Garmin Connect Mobile.

Store binaries download without authentication:
`https://services.garmin.com/appsLibraryBusinessServices_v0/rest/apps/<appId>/versions/<versionId>/binaries/<deviceKey>`.
Public metadata: `https://apps.garmin.com/api/appsLibraryExternalServices/api/asw/apps/<appId>`.

## Connect IQ settings definition (labels and dropdown values)

Garmin Express does not render app settings itself. It opens
`https://apps.garmin.com/en-US/appSettings2/<appId>/versions/<internalVersion>/devices/<partNumber>/edit?singlePage=true`
in its embedded browser. That page is public (no login) and server-rendered:
one `<label for=key>` plus a `<select>` (options carry the stored integer
values), a checkbox, a text input (`maxlength`, `readonly`) or a number
input (`min`, `max`, `step`) per setting. Express posts the device's `.SET`
to the page, and on save the page's `.../binary` endpoint returns a new
`.SET` that Express writes to `GARMIN/Apps/SETTINGS`. `internal/ciqstore`
fetches and parses that page (installed version first, then the store's
latest) and caches the result in `~/.cache/garminctl/appsettings/`.

## Installed apps (OUT.BIN)

Protobuf: root field 3 -> field 2 -> repeated field 3 `App { 1: 16-byte id,
2: list type (1 app, 2 glance, 3 watch face, 7 music provider, 8 activity),
3: name, 4: enabled, 5: version, 6: file name, 7: size, 8: native app id }`.
Built-in apps use ids like `c0ffee15-600d-20d9-...`.

## Software updates (OMT)

Garmin Express asks `POST https://omt.garmin.com/api/device-software/v1/updates`
with `{"GarminDeviceXml": <text of GARMIN/GarminDevice.xml>, "ClientInitiated":
true, "Locale": "en-US"}` (camelCase keys also work; no authentication,
verified 2026-10-22). The answer lists every deliverable for the device:
`partNumber`, `productName`, `version`/`displayVersion`, `isPrimaryFirmware`,
`dataType`, `severity`, `releaseNotes[{version, changeLog[]}]`, `eulaUrl`,
`deliverable{downloadUrl, md5, sizeInBytes}` and `installation{
isProductInstalled, path, fileName, isRestartRequired, isDelta, order}`.
Express downloads the file and copies it to `installation.path/fileName`
(golf course DBs go to `Garmin/RemoteSW/<name>.db`, deleting the old file
first); the watch installs it when the USB session ends. For this watch the
service currently offers only the four golf course databases, all installed.
`garmin.CheckSoftwareUpdates`/`InstallSoftwareUpdate` implement this.

Connect IQ apps: the public store record at
`apps.garmin.com/api/appsLibraryExternalServices/api/asw/apps/<appId>`
carries `latestInternalVersion` (the number in OUT.BIN) and
`latestExternalVersion` (the human version). The binary download
(`services.garmin.com/appsLibraryBusinessServices_v0/rest/apps/<id>/versions/<versionUUID>/binaries/<deviceKey>`)
needs the version UUID, which only a logged-in Express session gets, so the
tool detects updates and links to the store. Express installs apps by writing
`GARMIN/APPS/MEDIA/<8 hex>.PRG`; the watch registers it on disconnect
(`garmin.InstallPRG`).

Garmin Connect (`connect.garmin.com`) sends `x-frame-options: deny` and
redirects to SSO, so the web UI links to it in a new tab instead of embedding.

## Garmin Connect sync (what Express does with a login)

Reconstructed from `ExpressDetailed.log` (structure only) and from the
open-source garth client; implemented in `internal/gc` and
`internal/garmin/cloud.go`. Verified live on 2026-10-22: sign-in, profile,
device name (plain text answer), 202 uploads (200 recognised as duplicates
of what the phone had synced, 2 new), the true-up message Connect queued in
response, the sparse settings PUT producing a `settings-change` message
within seconds, pulling it to `GARMIN/NewFiles` (a type-41 file identical in
layout to the ones described above) and the acknowledgement
(`PUT …/message/<id>?status=received` answers `true`; a no-op PUT queues
nothing).

Sign-in (Garmin Connect Mobile flow): GET
`https://sso.garmin.com/mobile/sso/en/sign-in?clientId=GCM_ANDROID_DARK&locale=en-US&service=https://mobile.integration.garmin.com/gcm/android`
(cookies), POST `/mobile/api/login` with the same query and JSON
`{username, password, rememberMe:false, captchaToken:""}`;
`responseStatus.type` is `SUCCESSFUL` (ticket in `serviceTicketId`) or
`MFA_REQUIRED` (then POST `/mobile/api/mfa/verifyCode` with
`{mfaMethod, mfaVerificationCode, rememberMyBrowser:false, reconsentList:[],
mfaSetup:false}`). The ticket is traded at
`connectapi.garmin.com/oauth-service/oauth/preauthorized?ticket=…&login-url=…&accepts-mfa-tokens=true`
(OAuth 1.0 HMAC-SHA1, consumer key from thegarth.s3.amazonaws.com) for an
OAuth1 token (valid about a year), which is exchanged at
`oauth-service/oauth/exchange/user/2.0` for a bearer (about an hour; the
exchange is repeated to refresh). Express itself signs in through an embedded
browser and additionally holds a `services.garmin.com/api/oauth/token` bearer
for its app-store endpoints (`express/appstore/rest/apps/updates|downloads|installApps`);
those are not used here.

Uploads: Express asks `omt.garmin.com/UploadConfigurationService/UnitUploadSettings/<unitId>`
for a map of device-XML data type → upload URL. The map for this watch is
embedded in `cloud.go` (`uploadTargets`): FIT_TYPE_4 Activity →
`upload-service/upload/activity`, 32 Monitor and 49 Sleep → `/wellness`, 44
Metrics → `/metrics`, 68 HRVStatus → `/hrvstatus` (consent), 70 HSA →
`/partner`, 72 FbtBackup → `/lha-backup`, 74 FbtPtdBackup →
`/primary-training-backup` (consent), 79 SlpDisr → `/sleep-disruptions`
(consent), 41 ChngLog → `/changelog`, 38 Score → golf.garmin.com scorecard.
Multipart POST, field `file`; answers 201/202 (accepted), 409 (duplicate,
`detailedImportResult.failures[].messages[]`). Express deletes some types
after upload (Monitor); garminctl keeps a ledger
(`~/.cache/garminctl/uploaded-<unitId>.json`) instead and deletes nothing.

Downloads: `GET device-service/devicemessage/messages?device_id=<unitId>`
lists what Connect queued: `messageType` (`settings-change`, `install_app`,
`golf_club`, `personal-records`, `true_up`, workouts, courses…),
`deviceXmlDataType` (FIT_TYPE_41, IQWatchFaces…), `metaData.fileType`
(`FIT`, `BRAIN` = PRG), `metaData.messageUrl` (absolute, or relative to
connectapi), `metaData.absolute`. FIT files go to `GARMIN/NewFiles/<uuid>.FIT`,
app binaries to `GARMIN/APPS/MEDIA/<8 hex>.PRG`; then
`PUT device-service/devicemessage/message/<id>?status=received`
(answers `true`). App binaries come from
`services.garmin.com/appsLibraryBusinessServices_v0/rest/apps/<app>/versions/<versionUUID>/binaries/<deviceKey>?customerId=…&unitId=…&firmwareVersion=…`;
Connect queues them when an app is installed or updated on the store website
or by its auto-update.

Device settings in the cloud: `GET/PUT device-service/deviceservice/device-info/settings/<unitId>`
(`deviceId`, `timeFormat`, `dateFormat`, `measurementUnits`, `alarms[]`,
`language` + `supportedLanguages[]`, `mountingSide`, `autoSync…`,
`connectIQ.autoUpdate`, many nulls for unsupported keys). Express PUTs a
sparse object with only the changed key; Connect then queues a
`settings-change` message whose file is the type-41 FIT described above.

## Garmin Express internals worth knowing

- Logs: `C:\ProgramData\Garmin\Logs\Express\ExpressDetailed.log` contains
  full REST responses, including the device-settings JSON from
  `device-service/deviceservice/device-info/settings/<id>` (135 keys).
- Device settings in Express are a cloud round trip: Express sends the choice
  to Connect, Connect generates the type-41 file, Express downloads it.
- Express re-sends Garmin Connect's whole settings snapshot (83 field
  changes for this watch) on every sync; the phone app is expected to do the
  same over Bluetooth. Fields in that snapshot are marked `managedByConnect`.
