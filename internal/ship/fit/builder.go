package fit

import (
	"fmt"
	"math"
	"time"
)

// FITEpoch is 1989-12-31T00:00:00Z, the origin of FIT timestamps.
var FITEpoch = time.Date(1989, 12, 31, 0, 0, 0, 0, time.UTC)

// Timestamp converts t to a FIT timestamp. Times outside the uint32 range
// of the format (1989-12-31 to 2126-02-07) are clamped to its ends.
func Timestamp(t time.Time) uint32 {
	secs := t.Unix() - FITEpoch.Unix()
	secs = max(0, min(secs, math.MaxUint32))

	return uint32(secs)
}

// TimestampTime converts a FIT timestamp to time.Time.
func TimestampTime(ts uint32) time.Time {
	return FITEpoch.Add(time.Duration(ts) * time.Second)
}

// Builder constructs a FIT file using local message slot 0 and emits a
// definition record whenever the layout changes.
type Builder struct {
	file      *File
	bigEndian bool
	lastSig   string
	lastDef   *Definition
}

// NewBuilder returns a builder with big-endian record definitions.
func NewBuilder(profileVersion uint16, bigEndian bool) *Builder {
	return &Builder{
		file:      &File{ProtocolVersion: 0x10, ProfileVersion: profileVersion},
		bigEndian: bigEndian,
	}
}

// Write appends a data record. values maps field numbers to values; fields
// absent from values are written as invalid.
func (b *Builder) Write(globalNum uint16, fields []FieldDef, values map[uint8]any) error {
	if len(fields) > math.MaxUint8 {
		return fmt.Errorf("message %d: %d fields, a definition holds at most %d", globalNum, len(fields), math.MaxUint8)
	}

	sig := fmt.Sprintf("%d:%v", globalNum, fields)

	def := b.lastDef
	if def == nil || sig != b.lastSig {
		def = &Definition{LocalNum: 0, BigEndian: b.bigEndian, GlobalNum: globalNum, Fields: fields}
		b.lastSig = sig
		b.lastDef = def
		b.file.Records = append(b.file.Records, Record{Kind: RecDefinition, Def: def})
	}

	m := &Message{Def: def}
	for _, fd := range fields {
		f := Field{Def: fd, Raw: make([]byte, fd.Size)}

		v, has := values[fd.Num]
		if !has {
			v = nil
		}

		if fd.BaseType == BaseString {
			for i := range f.Raw {
				f.Raw[i] = 0
			}
		} else {
			inv := fd.BaseType.InvalidBytes(b.bigEndian)
			for i := 0; i+len(inv) <= len(f.Raw); i += len(inv) {
				copy(f.Raw[i:], inv)
			}
		}

		if err := f.SetValue(v, b.bigEndian); err != nil {
			return fmt.Errorf("message %d: %w", globalNum, err)
		}

		m.Fields = append(m.Fields, f)
	}

	b.file.Records = append(b.file.Records, Record{Kind: RecData, Msg: m})

	return nil
}

// File returns the file built so far.
func (b *Builder) File() *File { return b.file }

// Bytes encodes the file built so far.
func (b *Builder) Bytes() []byte { return b.file.Encode() }
