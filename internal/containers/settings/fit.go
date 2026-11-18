package settings

import (
	"fmt"
	"math"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/fit"
)

// FileTypeSettingsChange is the file_id.type of the settings-change files
// Garmin Connect drops into GARMIN/NewFiles.
const FileTypeSettingsChange = 41

// MesgSettingsChange is the undocumented message that carries one field
// change: 0 = mesg_num, 1 = message_index, 2 = field_num, 3 = array index,
// 4 = value, 253 = timestamp.
const MesgSettingsChange = 217

// BuildChangeFile produces a settings-change FIT file the watch applies
// from GARMIN/NewFiles. unitID and swVersion come from the device.
func BuildChangeFile(changes []ResolvedChange, unitID uint64, swVersion uint16, now time.Time) ([]byte, error) {
	b := fit.NewBuilder(21217, true)

	ts := fit.Timestamp(now)
	if err := b.Write(0, []fit.FieldDef{
		{Num: 0, Size: 1, BaseType: fit.BaseEnum},
		{Num: 1, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 2, BaseType: fit.BaseUint16},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
		{Num: 3, Size: 4, BaseType: fit.BaseUint32z},
		{Num: 5, Size: 2, BaseType: fit.BaseUint16},
	}, map[uint8]any{0: FileTypeSettingsChange, 1: 1, 2: 65534, 4: ts, 3: fitSerial(unitID), 5: 1}); err != nil {
		return nil, err
	}

	if err := b.Write(49, []fit.FieldDef{
		{Num: 1, Size: 1, BaseType: fit.BaseUint8},
		{Num: 0, Size: 2, BaseType: fit.BaseUint16},
	}, map[uint8]any{1: 0, 0: swVersion}); err != nil {
		return nil, err
	}

	simple := []fit.FieldDef{
		{Num: 253, Size: 4, BaseType: fit.BaseUint32},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
		{Num: 0, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 1, BaseType: fit.BaseUint8},
	}
	indexed := []fit.FieldDef{
		{Num: 253, Size: 4, BaseType: fit.BaseUint32},
		{Num: 4, Size: 4, BaseType: fit.BaseUint32},
		{Num: 0, Size: 2, BaseType: fit.BaseUint16},
		{Num: 1, Size: 2, BaseType: fit.BaseUint16},
		{Num: 2, Size: 1, BaseType: fit.BaseUint8},
		{Num: 3, Size: 1, BaseType: fit.BaseUint8},
	}

	for _, c := range changes {
		if err := validateChangeIndices(c.Change); err != nil {
			return nil, err
		}

		if c.Raw < math.MinInt32 || c.Raw > math.MaxUint32 {
			return nil, fmt.Errorf("raw value %d does not fit the settings-change slot", c.Raw)
		}

		// The value slot is a uint32; Resolve keeps Raw within int32..uint32,
		// and a negative value travels as its two's complement.
		raw := uint32(c.Raw)

		vals := map[uint8]any{253: ts, 4: raw, 0: c.MesgNum, 2: c.FieldNum}
		if c.ArrayIndex != nil || c.Index > 0 {
			vals[1] = c.Index

			ai := 0
			if c.ArrayIndex != nil {
				ai = *c.ArrayIndex
			}

			vals[3] = ai
			if err := b.Write(MesgSettingsChange, indexed, vals); err != nil {
				return nil, err
			}
		} else {
			if err := b.Write(MesgSettingsChange, simple, vals); err != nil {
				return nil, err
			}
		}
	}

	return b.Bytes(), nil
}

// fitSerial preserves the existing file_id serial-number boundary: FIT stores
// the low 32 bits of a device's uint64 UnitID. This truncation is deliberate at
// the protocol boundary, not implicit narrowing inside the scalar codec.
func fitSerial(unitID uint64) uint32 { return uint32(unitID & math.MaxUint32) }
