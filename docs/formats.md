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

The page exists only for apps that ship a `settings.xml`. For the others it
answers 404 (verified 2026-10-22 for YouTube Music, the Connect IQ Store app
and Komoot). YouTube Music still has a `.SET` with its internal properties
(`SYNC_PAGE_SIZE`, `syncType`, `BASE_YMCA_URL`, `EPISODE_STREAM_FORMAT`,
`BASE_OAUTH_URL`), which the web UI shows read-only with a notice; the Store
app and Komoot have no `.SET` at all. `app-settings` on the CLI can still
write raw values for such apps.

## Installed apps (OUT.BIN)

Protobuf: root field 3 -> field 2 -> repeated field 3 `App { 1: 16-byte id,
2: list type (1 app, 2 glance, 3 watch face, 7 music provider, 8 activity),
3: name, 4: flag, 5: version, 6: file name, 7: size, 8: native app id }`.
Built-in apps use ids like `c0ffee15-600d-20d9-...`. The user's watch lists
139 entries (64 activities, 33 glances, 40 apps, the watch face, YouTube
Music); the registry has no "in the activity list / glance loop" marker.

`IN.BIN` is the same protobuf handed back by the host; the watch applies it
on unplug and deletes it (verified: IN.BIN gone and OUT.BIN updated after
the replug). `SetAppHidden` re-encodes the registry byte for byte with one
field-4 value changed (identity re-encoding is checked first, a copy of
OUT.BIN is kept in `~/.cache/garminctl`) and writes it as IN.BIN, using a
pending IN.BIN as the base so several changes queue up; openExpress
toggles built-in activities and glances the same way. Installing a `.PRG` needs no IN.BIN entry: the watch
registered Simply Large from the file alone.

Field 4 means "hidden": set when the entry is not in the user's activity
list, glance loop or app list. Verified 2026-10-22: `app-show Archery`
cleared the bit in IN.BIN, after the unplug Archery appeared in the activity
list on the watch and OUT.BIN carried the cleared bit. On the user's watch
the built-in entries he uses (Walk, Boxing, Yoga, the Steps/Weather/Heart
Rate/Body Battery glances, Komoot) have it clear; unused ones and the
Connect IQ watch face and music provider have it set. The Apps & glances
tab shows it as Shown/Hidden with Hide/Show buttons; `app-hide`,
`app-show`, `app-pending` on the CLI; `watch_set_app_hidden` for MCP.

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

## Health and workouts (Garmin Connect)

With the session from `internal/gc`, these connectapi paths are used (all
verified live on 2026-10-22): `GET /usersummary-service/usersummary/daily/<displayName>?calendarDate=D`
(totalSteps, dailyStepGoal, restingHeartRate, min/maxHeartRate,
averageStressLevel, bodyBatteryHighest/LowestValue, sleepingSeconds,
totalKilocalories, activeKilocalories, floorsAscended, moderate/vigorous
intensity minutes), `GET /wellness-service/wellness/dailyHeartRate/<displayName>?date=D`
(`heartRateValues[[ms, bpm]]`), `GET /wellness-service/wellness/dailyStress/D`
(`stressValuesArray[[ms, level]]`, `bodyBatteryValuesArray[[ms, status, level, …]]`),
`GET /wellness-service/wellness/dailySleepData/<displayName>?date=D&nonSleepBufferMinutes=60`
(`dailySleepDTO` with stage seconds and `sleepScores.overall`, `sleepLevels[{startGMT, activityLevel}]`),
`GET /workout-service/workouts?start&limit&myWorkoutsOnly=true`,
`POST /workout-service/workout` (Connect workout JSON; answers `workoutId`),
`DELETE /workout-service/workout/<id>`, and the device-message push
`POST /device-service/devicemessage/messages` with
`[{deviceId, messageUrl: "workout-service/workout/FIT/<id>", messageType: "workouts", messageName, priority: 1, fileType: "FIT", metaDataId: <id>, groupName: null}]`,
after which the queue holds the workout FIT. `displayName` is the opaque id
from `/userprofile-service/socialProfile`.

Workout FIT (type 5): `workout` (sport, sub_sport, num_valid_steps,
wkt_name) and `workout_step` messages: duration_type time=0 (ms),
distance=1 (cm), calories=4, open=5, repeat_until_steps_cmplt=6
(duration_value = index of the first step of the loop, target_value =
repetitions); target_type speed=0 (custom low/high in mm/s), heart_rate=1
(target_value = zone, or 0 with custom low/high = bpm + 100), open=2,
cadence=3, power=4 (custom = watts + 1000); intensity active=0, rest=1,
warmup=2, cooldown=3, recovery=4, interval=5. Connect's own file for the
same spec matched this layout field for field.

## Connect IQ store (public website API)

`https://apps.garmin.com/api/appsLibraryExternalServices/api/asw` answers
without authentication: `GET /deviceTypes` (id, partNumber, name; the
vívoactive 5 is id 294), `GET /apps?appType=watchface|watch-app|widget|datafield|audio-content-provider-app&partNumber=…&sortType=mostPopular|highestRated|mostRecent|trending&startPageIndex=N&pageSize=N&countryCode=US`
(array of records), `GET /apps/keywords?keywords=…&startPageIndex=N&pageSize=N&countryCode=US&locale=en-US`
(`{totalCount, apps[], appTypeNames}`), `GET /apps/<id>?countryCode=US`
(full record: `appLocalizations[{locale,name,description,whatsNew}]`,
`latestExternalVersion`, `latestInternalVersion`, `averageRating`,
`reviewCount`, `downloadCount`, `pricing.salePrice.formattedPrice`,
`fileSizeInfo.byteCountByDeviceTypeId`, `settingsAvailabilityInfo`,
`permissions`, `compatibleDevicePartNumbers`, `hasTrialMode`), and
`GET /apps/<id>/reviews?pageSize=N&startPageIndex=0`. Record `typeId` is
1 watchface, 2 app, 3 widget, 4 datafield, 5 music (not the device's OUT.BIN
numbering). Icons: `https://services.garmin.com/appsLibraryBusinessServices_v0/rest/apps/<id>/icon/<iconFileId>`
(public PNG); the screenshot URL pattern was not found. `startPageIndex` is
a record offset. Implemented in `internal/ciqstore/store.go`.

The vívoactive 5 keeps `GARMIN/Apps/Media` (the PRG files of installed
apps) out of the ordinary MTP listing, like the fēnix 8, and refuses to
create the folder (E_FAIL). Garmin's vendor PTP operation 0x9002
`GetObjectIds(storage, parent)` returns the hidden handles (same array
format as GetObjectHandles; the folder's own files answer only to 0x9002,
the standard listing of the folder is empty). The Windows MTP driver passes
raw operations through WPD's MTP extension commands
(`WPD_COMMAND_MTP_EXT_EXECUTE_COMMAND_WITH_DATA_TO_READ/WRITE`, object ids
are `o<hex handle>`, storage `s<hex id>`; the driver does not know the hidden
handles itself, so `EnumObjects("o…")` fails). Writing is
SendObjectInfo(storage, parent) with an ObjectInfo dataset (format and thumb
format 0x3000, as Express sends it) followed by SendObject; the new file
shows in the ordinary listing with its size, reads back transformed (not
byte-identical), and DeleteObject(handle) removes it. `GetStorageInfo` (0x1005) over the same raw path gives capacity and free
space (3 706 159 104 and 1 844 510 720 bytes on 2026-10-22, "Internal
Storage"). Verified 2026-10-22:
"Simply Large" queued from the Connect IQ phone app (install_app message,
fileType BRAIN, deviceXmlDataType IQWatchFaces, binary from
`services.garmin.com/appsLibraryBusinessServices_v0/rest/apps/<id>/versions/<v>/binaries/vivoactive5?...`)
was written as `57BF662A.PRG` and the watch registered it in OUT.BIN on
unplug with no IN.BIN entry. Implemented in `internal/device/wpd/raw.go`
(`device.HiddenFS`) and `Service.WriteAppFile`. The store website no longer
offers a Download button (it points to the phone app); the phone app queues
the install server-side when it cannot reach the watch.

Audio content provider apps live in the same hidden area. The device XML's
`Extensions/IQAppExt/Apps` list gives each Connect IQ app its
`AppType`; providers (`audio-content-provider-app`, e.g. YouTube Music) add
`<MediaCacheSize>` in bytes, the size of their DRM download cache
(104 005 632 B for the user's 15 YouTube Music tracks on 2026-10-22).
`Music/`, `Podcasts` and `Audiobooks` hold only files the user copies, so
provider downloads cannot be listed, copied or deleted over MTP; the Music
tab, `music ls` and `music_list` report them as "providers" by size only.

## More Connect endpoints (health extras and history, verified 2026-10-22)

- Range stats, used for anything over 14 days (every one of them refuses
  spans over 28 days, so the tool asks in 28-day windows and caches days
  older than two days in `~/.cache/garminctl/history-<unit>.json`): steps
  `GET /usersummary-service/stats/steps/daily/{start}/{end}`
  (`calendarDate, totalSteps, totalDistance, stepGoal`); resting heart rate
  `GET /userstats-service/wellness/daily/{displayName}?fromDate&untilDate&metricId=60`
  (`allMetrics.metricsMap.WELLNESS_RESTING_HEART_RATE[]`); stress
  `GET /usersummary-service/stats/stress/daily/{start}/{end}`
  (`values.overallStressLevel`); body battery
  `GET /wellness-service/wellness/bodyBattery/reports/daily?startDate&endDate`
  (`bodyBatteryValuesArray[[ms, level]]`); sleep
  `GET /sleep-service/stats/sleep/daily/{start}/{end}`
  (`individualStats[].values.totalSleepTimeInSeconds`, plus `overallStats`);
  sleep score only: `GET /wellness-service/stats/daily/sleep/score/{start}/{end}`.
  `/wellness-service/stats/sleep/daily/...` is 404.
- VO2 max: `GET /metrics-service/metrics/maxmet/daily/{start}/{end}` →
  `generic.vo2MaxPreciseValue`; fitness age:
  `GET /fitnessage-service/fitnessage/{date}` → `fitnessAge, chronologicalAge`;
  HRV: `GET /hrv-service/hrv/{date}` → `hrvSummary{weeklyAvg, lastNightAvg, status}`.
- Personal records: `GET /personalrecord-service/personalrecord/prs/{displayName}`
  → `typeId, value, activityId, activityType, prStartTimeGmtFormatted`. Type
  ids seen: 1 (1 km run, seconds), 7 (longest run, m), 8 (longest ride, m),
  9 (cycling, 637 on the day a "Climber 1" badge was earned: probably biggest
  climb in m, unverified), 12/13/14 (most steps in a day/week/month), 15
  (longest goal streak, days), 16 (value 0, unknown).
- Badges: `GET /badge-service/badge/earned` → `badgeName, badgeKey,
  badgeEarnedDate, badgePoints, badgeEarnedNumber`.
- Weight: `GET /weight-service/weight/dateRange?startDate&endDate` →
  `dateWeightList[]` (empty on this account); manual weigh-in
  `POST /weight-service/user-weight` with `{dateTimestamp, gmtTimestamp
  ("2006-01-02T15:04:05.00"), unitKey: "kg", sourceType: "MANUAL", value}`
  (shape from python-garminconnect, not yet exercised here).
- Gear: `GET /gear-service/gear/filterGear?userProfilePk={profileId}` (empty).
- Express's app-store API (`services.garmin.com/express/appstore/rest/apps/
  downloads|updates|installApps|removeInstalledApps`, OAuth1) answers 403
  "X-garmin-client-id-header not found" for a token that is not Garmin
  Express's own, so the Store tab cannot queue installs; the Connect IQ phone
  app queues them and sync delivers.

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
