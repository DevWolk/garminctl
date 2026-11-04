package workouts

import "math"

// fitSerial preserves the existing file_id serial-number boundary: FIT stores
// the low 32 bits of a device's uint64 UnitID. This truncation is deliberate at
// the protocol boundary, not implicit narrowing inside the scalar codec.
func fitSerial(unitID uint64) uint32 { return uint32(unitID & math.MaxUint32) }
