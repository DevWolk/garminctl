# File formats and transport

This reference describes the codecs and transfer rules in `internal/fit`,
`internal/ciq`, `internal/device` and `internal/garmin`. Fixture tests cover the
included samples; device contents and firmware behavior can differ.

## Device paths and transport

Paths are relative to device storage. [`GarminDevice.xml`](../internal/garmin/devicexml.go)
describes data types, folders and transfer directions; use those capabilities
rather than assuming that every device has the same files.

| Purpose | Path used by the service | Handling |
| --- | --- | --- |
| Capabilities and software inventory | `GARMIN/GarminDevice.xml`, `GARMIN/Device.fit` | Read device metadata and software messages. |
| Device settings | `GARMIN/Settings/Settings.fit` | Read; write changes through `NewFiles`. |
| Pending FIT input | `GARMIN/NewFiles/<uuid>.FIT` | Settings changes, workouts and downloaded FIT messages. |
| Sport profiles | `GARMIN/Sports/*.fit` | FIT files. |
| App settings | `GARMIN/Apps/SETTINGS/<file>.SET` | Read and rewrite the settings dictionary. |
| App registry | `GARMIN/Apps/OUT.BIN`, `GARMIN/Apps/IN.BIN` | Read current entries; stage visibility changes in `IN.BIN`. |
| App binaries | `GARMIN/APPS/MEDIA/<file>.PRG` | Use hidden-object operations when needed. |
| Activities and local audio | `GARMIN/Activity`, `Music`, `Audiobooks`, `Podcasts` | Activity FIT files and user-managed audio. |

The [WPD bridge](../internal/device/wpd/client.go) exchanges JSON lines with a
PowerShell/C# helper on Windows, including Windows PowerShell through WSL.
Binary payloads are base64. Automatic selection requires manufacturer `Garmin`
or USB vendor `091e` in the device ID; an explicit ID must match exactly.

The [native Linux transport](../internal/device/mtp/usb_linux.go) uses libusb
with CGO, selects vendor `091e`, and claims an MTP interface. It does not detach
kernel drivers or reset USB. Its protocol tests use a simulated wire and do not
establish physical USB compatibility. [`DirFS`](../internal/device/dirfs.go)
provides offline access through `os.Root`, with case-insensitive path matching
and rejection of paths escaping the root.

Both MTP implementations combine standard `GetObjectHandles` (`0x1007`)
with Garmin `GetObjectIds` (`0x9002`) for hidden listings. File writes use
`SendObjectInfo` (`0x100C`) followed by `SendObject` (`0x100D`), with object
and thumbnail formats `0x3000`. Replacements delete the existing object first.
Hidden WPD writes and native Linux writes check the reported size after transfer.
They do not compare the downloaded bytes with the uploaded bytes.
See [WPD raw operations](../internal/device/wpd/raw.go) and the
[native filesystem implementation](../internal/device/mtp/device.go).

The native transport serializes complete operations, including two-command writes.
The [protocol](../internal/device/mtp/protocol.go) invalidates the session after
transfer or framing failures; a protocol response error alone does not.
Keep command/data framing,
transaction IDs and zero-length packet handling intact.

## FIT codec

[`internal/fit`](../internal/fit/) retains record order, local definitions,
field bytes, developer fields and compressed timestamp headers. Definitions
select their byte order. The profile names documented fields; raw unknown fields
remain available without adding a profile entry.

`Decode` accepts 12- or 14-byte headers. It checks nonzero header CRCs in
14-byte headers and file CRCs when present, returning decoded data alongside
CRC errors. `Encode` writes a 14-byte header and recalculates both CRCs, so it
normalizes those details. [Round-trip tests](../internal/fit/fit_test.go) require
byte-identical output for the included fixtures.

`Builder` uses local slot 0 and emits a definition whenever the message number
or field layout changes. Absent values use FIT invalid values. Field edits keep
the existing width; scalar writes affect the first array element, while
`SetElement` addresses an individual element. Notes on undocumented settings
live in [device_settings.json](../internal/containers/settings/knowledge/device_settings.json).
Explicit integer edits reject values outside the base type's signed/unsigned
range while allowing its FIT invalid pattern. Failed edits leave `Raw` unchanged,
including a failure late in an array; a shorter array edit retains the tail.
These mutation checks do not reinterpret untouched unknown field bytes.

## Settings-change files

[`ApplyChanges`](../internal/garmin/settings.go) writes
`GARMIN/NewFiles/<uuid>.FIT` without rewriting `Settings.fit`. End the USB
session and check the setting on the watch; a directory dump only records the
pending file.

`BuildChangeFile` uses big-endian FIT profile 21.217 and writes:

- `file_id` (message 0): type 41, manufacturer 1, product 65534, creation
  timestamp, device serial number and file number 1.
- `file_creator` (message 49): hardware version 0 and the device software version.
- One message 217 for each resolved change, using the fields below.

| Field | Type | Meaning |
| --- | --- | --- |
| 0 | `uint16` | Target message number. |
| 1 | `uint16` | Message index, included with indexed changes. |
| 2 | `uint8` | Target field number. |
| 3 | `uint8` | Array element index, included with indexed changes. |
| 4 | `uint32` | Unscaled value; negative integers use two's complement. |
| 253 | `uint32` | FIT timestamp. |

Resolution applies profile scaling and accepts numeric identifiers for
undocumented messages and fields. String values cannot use this change format;
integers must fit the signed-32-bit through unsigned-32-bit range.
Message/array indices must fit their unsigned 16-/8-bit slots. Nonfinite numbers
and fractional raw integers are rejected; profile scaling retains its existing
rounding before the range check.
Both settings and workout builders propagate FIT construction errors before
staging a file. Their `file_id` serial field deliberately stores the low 32 bits
of the device's uint64 UnitID, preserving the existing protocol representation.

## Connect IQ settings

[`internal/ciq`](../internal/ciq/ciq.go) implements big-endian serialization:

1. Optional string block: `0xABCDABCD`, `uint32` byte length, then strings
   with a `uint16` length counting UTF-8 bytes and the NUL terminator.
2. Data block: `0xDA7ADA7A`, `uint32` byte length, then one typed value.

| Type code | Value | Payload |
| --- | --- | --- |
| `0x00` | Null | None. |
| `0x01`, `0x02` | Integer, float | `int32`, `float32`. |
| `0x03` | String | `uint32` offset into the string block. |
| `0x05` | Array | `uint32` count, then values. |
| `0x09` | Boolean | One byte. |
| `0x0B` | Dictionary | `uint32` count, then key/value pairs. |
| `0x0E`, `0x0F` | Long, double | `int64`, `float64`. |

Dictionaries preserve insertion order; encoding interns strings in order of
first appearance. The [fixture tests](../internal/ciq/ciq_test.go) require
byte-identical round trips. Converting a dictionary to a JSON object loses order.

[`SetAppSettings`](../internal/garmin/service.go) edits existing keys, preserves
stored value types, rewrites the `.SET` file and reads it back. When a
[settings schema](../internal/garmin/appschema.go) is available, it resolves
option labels and checks read-only fields, numeric bounds and text length.
Without a schema, it coerces values to the existing types.

[`internal/ciqstore`](../internal/ciqstore/ciqstore.go) fetches the settings page
at `apps.garmin.com/en-US/appSettings2/<app>/versions/<version>/devices/<part>/edit?singlePage=true`.
It parses labels, dropdown values and input constraints. Schema lookup tries
the installed version, then the latest store version, and caches successful
results. A missing schema does not imply a missing `.SET` file.

## App registry and binaries

[`ParseAppsConfig`](../internal/garmin/appsconfig.go) reads protobuf nesting
`root.3 -> config.2 -> repeated list.3`. Each app contains:

| Field | Encoding | Meaning |
| --- | --- | --- |
| 1 | Bytes | 16-byte app ID. |
| 2 | Varint | Registry type: 1 app, 2 glance, 3 watchface, 4 datafield, 5 widget, 6 audio, 7 music, 8 activity. |
| 3 | String | Name. |
| 4 | Varint | Hidden flag; nonzero means hidden. |
| 5 | Varint | Internal version. |
| 6 | String | File name for a Connect IQ app. |
| 7 | Varint | Size. |
| 8 | Varint | Native app ID. |

[`SetAppHidden`](../internal/garmin/appsconfig_write.go) uses pending `IN.BIN`
as its base when present, otherwise `OUT.BIN`. It refuses writes if identity
re-encoding changes any bytes, changes field 4 for the matched entry, writes
and reads back a private `OUT.BIN` backup, then writes `IN.BIN`. Backup failure
prevents the device write. Finish the USB session before checking
visibility on the watch.

[`InstallPRG`](../internal/garmin/updates.go) performs a minimal header check
and writes a randomly named `.PRG` through `WriteAppFile`; it does not parse
or validate every PRG section. A visible app folder uses ordinary writes;
otherwise the service requires `device.HiddenFS`. Installation does not add
an `IN.BIN` entry.

## Workout FIT

[`WorkoutSpec.FIT`](../internal/garmin/workouts.go) writes type-5 FIT with
`workout` (26) and `workout_step` (27) messages. The encoding uses:

- Duration: time 0 in milliseconds, distance 1 in centimetres, calories 4,
  open 5, repeat 6. A repeat stores the first step index as `duration_value`
  and the repetition count as `target_value`.
- Target: speed 0 in millimetres/second, heart rate 1, open 2, cadence 3,
  power 4. Heart-rate zones use `target_value`; custom ranges use 0 there,
  with heart rate encoded as bpm + 100 and power as watts + 1000.
- Intensity: main 0, rest 1, warmup 2, cooldown 3, recovery 4, interval 5,
  other 6.

Workout validation checks duration, distance and target units before converting
them to unsigned FIT slots; existing fractional-unit truncation is retained.
It also checks repeat counts, flattened step counts and the Connect duration
estimate. Duration text must match complete decimal/unit groups or colon
components, so unmatched signs/text cannot be silently discarded. Invalid input
returns an error before a workout is staged or sent to Connect.

## Network transfers and local state

The implementation makes these network requests:

| Area | Implementation and behavior |
| --- | --- |
| Authentication | [`internal/gc`](../internal/gc/client.go) uses mobile SSO with MFA, OAuth1 preauthorization and OAuth2 bearer exchange. Token responses supply expiry times. |
| Software updates | [`updates.go`](../internal/garmin/updates.go) POSTs `{"GarminDeviceXml": "..."}` to `https://omt.garmin.com/api/device-software/v1/updates`. Downloads check supplied size and MD5 before writing the manifest's installation path. |
| Store | [`ciqstore/store.go`](../internal/ciqstore/store.go) requests public app metadata, search, device types and reviews under `apps.garmin.com/api/appsLibraryExternalServices/api/asw`. `startPageIndex` is a record offset. Store type IDs differ from registry type IDs. |
| Cloud sync | [`cloud.go`](../internal/garmin/cloud.go) reads and updates device settings, downloads device messages, and uploads supported data types using `uploadTargets`. |
| Health and workouts | [`health.go`](../internal/garmin/health.go), [`health_more.go`](../internal/garmin/health_more.go) and [`workouts.go`](../internal/garmin/workouts.go) define the Connect endpoints and payloads. |
| Audio providers | [`music.go`](../internal/garmin/music.go) reports provider names and cache sizes from device XML separately from local audio files. |

Cloud downloads route `FIT` to `NewFiles`, `BRAIN`/`PRG` to the app folder,
and other types to an input-capable XML path. Messages are acknowledged only
after a successful write; unknown destinations remain queued. Uploads retain
device files and record accepted or duplicate files in a per-device ledger.
App update checks compare store metadata; the store browser does not queue
app installations.

[`paths.Cache`](../internal/paths/paths.go) uses the OS cache directory, falling
back to the temporary directory, under `garminctl/`. It holds settings schemas,
registry backups and per-device caches. The default Connect token store uses
`os.UserConfigDir()/garminctl/garmin-connect.json`, falling back to the current
directory if no config directory is available.
