// Package fit implements a lossless codec for Garmin FIT files.
//
// The codec keeps every record, including undocumented messages and fields,
// as raw bytes so that a decoded file re-encodes byte-for-byte. A profile
// layer (see Profile) names the messages and fields Garmin documents in the
// public FIT SDK.
package fit

import (
	"encoding/binary"
	"fmt"
	"math"
	"slices"
)

// BaseType is a FIT base type code as stored in a definition record.
type BaseType uint8

// Base type codes as the FIT protocol stores them in definition records;
// bit 7 marks the multi-byte types.
const (
	BaseEnum    BaseType = 0x00
	BaseSint8   BaseType = 0x01
	BaseUint8   BaseType = 0x02
	BaseSint16  BaseType = 0x83
	BaseUint16  BaseType = 0x84
	BaseSint32  BaseType = 0x85
	BaseUint32  BaseType = 0x86
	BaseString  BaseType = 0x07
	BaseFloat32 BaseType = 0x88
	BaseFloat64 BaseType = 0x89
	BaseUint8z  BaseType = 0x0A
	BaseUint16z BaseType = 0x8B
	BaseUint32z BaseType = 0x8C
	BaseByte    BaseType = 0x0D
	BaseSint64  BaseType = 0x8E
	BaseUint64  BaseType = 0x8F
	BaseUint64z BaseType = 0x90
)

var baseTypeNames = map[BaseType]string{
	BaseEnum: "enum", BaseSint8: "sint8", BaseUint8: "uint8", BaseSint16: "sint16", BaseUint16: "uint16",
	BaseSint32: "sint32", BaseUint32: "uint32", BaseString: "string", BaseFloat32: "float32", BaseFloat64: "float64",
	BaseUint8z: "uint8z", BaseUint16z: "uint16z", BaseUint32z: "uint32z", BaseByte: "byte", BaseSint64: "sint64",
	BaseUint64: "uint64", BaseUint64z: "uint64z",
}

var baseTypeByName = func() map[string]BaseType {
	m := make(map[string]BaseType, len(baseTypeNames))
	for k, v := range baseTypeNames {
		m[v] = k
	}

	return m
}()

// BaseTypeByName returns the base type for a profile name such as "uint16".
func BaseTypeByName(name string) (BaseType, bool) {
	bt, ok := baseTypeByName[name]
	return bt, ok
}

func (b BaseType) String() string {
	if n, ok := baseTypeNames[b]; ok {
		return n
	}

	return fmt.Sprintf("base0x%02X", uint8(b))
}

// Size is the size in bytes of one element. Strings and bytes return 1.
func (b BaseType) Size() int {
	switch b {
	case BaseSint16, BaseUint16, BaseUint16z:
		return 2
	case BaseSint32, BaseUint32, BaseUint32z, BaseFloat32:
		return 4
	case BaseFloat64, BaseSint64, BaseUint64, BaseUint64z:
		return 8
	default:
		return 1
	}
}

// Known reports whether the code is a base type defined by the FIT protocol.
func (b BaseType) Known() bool {
	_, ok := baseTypeNames[b]
	return ok
}

// FieldDef is one field in a definition record.
type FieldDef struct {
	Num      uint8
	Size     uint8
	BaseType BaseType
}

// DevFieldDef is one developer field in a definition record.
type DevFieldDef struct {
	Num      uint8
	Size     uint8
	DevIndex uint8
}

// Definition is a definition record. It describes the layout of the data
// records that follow it under the same local message number.
type Definition struct {
	LocalNum  uint8
	BigEndian bool
	GlobalNum uint16
	Fields    []FieldDef
	DevFields []DevFieldDef
}

// DataSize is the size in bytes of one data record using this definition.
func (d *Definition) DataSize() int {
	n := 0
	for _, f := range d.Fields {
		n += int(f.Size)
	}

	for _, f := range d.DevFields {
		n += int(f.Size)
	}

	return n
}

// Field is a field value inside a data record, kept as raw bytes.
type Field struct {
	Def FieldDef
	Raw []byte
}

// DevField is a developer field value inside a data record.
type DevField struct {
	Def DevFieldDef
	Raw []byte
}

// Message is one data record.
type Message struct {
	Def       *Definition
	Fields    []Field
	DevFields []DevField
	// Compressed is set for records written with a compressed timestamp header.
	Compressed bool
	TimeOffset uint8
}

// Field returns the field with the given number.
func (m *Message) Field(num uint8) (*Field, bool) {
	for i := range m.Fields {
		if m.Fields[i].Def.Num == num {
			return &m.Fields[i], true
		}
	}

	return nil, false
}

// Value decodes a field by its number. It returns nil when the field is
// absent or holds the FIT invalid value.
func (m *Message) Value(num uint8) any {
	f, ok := m.Field(num)
	if !ok {
		return nil
	}

	return f.Value(m.Def.BigEndian)
}

// RecordKind distinguishes definition and data records in the stream.
type RecordKind uint8

// Record kinds of the FIT data stream.
const (
	RecDefinition RecordKind = iota
	RecData
)

// Record is one record of the FIT data stream, in file order.
type Record struct {
	Kind RecordKind
	Def  *Definition // for RecDefinition
	Msg  *Message    // for RecData
}

// File is a decoded FIT file.
type File struct {
	ProtocolVersion uint8
	ProfileVersion  uint16
	Records         []Record
}

// Messages returns the data records in file order.
func (f *File) Messages() []*Message {
	var out []*Message

	for _, r := range f.Records {
		if r.Kind == RecData {
			out = append(out, r.Msg)
		}
	}

	return out
}

// MessagesByNum returns the data records with the given global message number.
func (f *File) MessagesByNum(num uint16) []*Message {
	var out []*Message

	for _, r := range f.Records {
		if r.Kind == RecData && r.Msg.Def.GlobalNum == num {
			out = append(out, r.Msg)
		}
	}

	return out
}

// Invalid reports whether the raw element holds the FIT "invalid" value.
func (b BaseType) Invalid(raw []byte, bigEndian bool) bool {
	switch b {
	case BaseEnum, BaseUint8, BaseByte:
		return raw[0] == 0xFF
	case BaseSint8:
		return raw[0] == 0x7F
	case BaseUint8z, BaseUint16z, BaseUint32z, BaseUint64z:
		for _, c := range raw {
			if c != 0 {
				return false
			}
		}

		return true
	case BaseUint16, BaseUint32, BaseUint64, BaseFloat32, BaseFloat64:
		for _, c := range raw {
			if c != 0xFF {
				return false
			}
		}

		return true
	case BaseSint16:
		return readUint(raw, bigEndian) == 0x7FFF
	case BaseSint32:
		return readUint(raw, bigEndian) == 0x7FFFFFFF
	case BaseSint64:
		return readUint(raw, bigEndian) == 0x7FFFFFFFFFFFFFFF
	case BaseString:
		return len(raw) == 0 || raw[0] == 0
	}

	return false
}

// InvalidBytes returns the FIT invalid value pattern for one element.
func (b BaseType) InvalidBytes(bigEndian bool) []byte {
	n := b.Size()
	out := make([]byte, n)

	switch b {
	case BaseUint8z, BaseUint16z, BaseUint32z, BaseUint64z, BaseString:
		return out
	case BaseSint8:
		out[0] = 0x7F
	case BaseSint16:
		writeUint(out, 0x7FFF, bigEndian)
	case BaseSint32:
		writeUint(out, 0x7FFFFFFF, bigEndian)
	case BaseSint64:
		writeUint(out, 0x7FFFFFFFFFFFFFFF, bigEndian)
	default:
		for i := range out {
			out[i] = 0xFF
		}
	}

	return out
}

func readUint(raw []byte, bigEndian bool) uint64 {
	var v uint64

	if bigEndian {
		for _, c := range raw {
			v = v<<8 | uint64(c)
		}
	} else {
		for _, r := range slices.Backward(raw) {
			v = v<<8 | uint64(r)
		}
	}

	return v
}

func writeUint(dst []byte, v uint64, bigEndian bool) {
	n := len(dst)
	for i := range n {
		b := byte(v >> (8 * uint(i))) //nolint:gosec // serialises byte i of v
		if bigEndian {
			dst[n-1-i] = b
		} else {
			dst[i] = b
		}
	}
}

// Value decodes the field. Scalars decode to int64, uint64, float64 or
// string; arrays decode to []any. Invalid elements decode to nil.
func (f *Field) Value(bigEndian bool) any {
	bt := f.Def.BaseType
	if bt == BaseString {
		return decodeString(f.Raw)
	}

	size := bt.Size()
	if !bt.Known() || size == 0 || len(f.Raw)%size != 0 {
		return append([]byte(nil), f.Raw...)
	}

	count := len(f.Raw) / size
	if count == 1 {
		return decodeScalar(bt, f.Raw, bigEndian)
	}

	out := make([]any, count)
	for i := range count {
		out[i] = decodeScalar(bt, f.Raw[i*size:(i+1)*size], bigEndian)
	}

	return out
}

// Count returns the number of elements in the field (1 for strings).
func (f *Field) Count() int {
	bt := f.Def.BaseType
	if bt == BaseString || !bt.Known() {
		return 1
	}

	return len(f.Raw) / bt.Size()
}

func decodeString(raw []byte) any {
	end := 0
	for end < len(raw) && raw[end] != 0 {
		end++
	}

	if end == 0 {
		return nil
	}

	return string(raw[:end])
}

func decodeScalar(bt BaseType, raw []byte, bigEndian bool) any {
	if bt.Invalid(raw, bigEndian) {
		return nil
	}

	u := readUint(raw, bigEndian)

	//nolint:gosec // the raw bytes hold a two's-complement sintN; reinterpreting the bits is the decoding
	switch bt {
	case BaseSint8:
		return int64(int8(u))
	case BaseSint16:
		return int64(int16(u))
	case BaseSint32:
		return int64(int32(u))
	case BaseSint64:
		return int64(u)
	case BaseFloat32:
		return float64(math.Float32frombits(uint32(u)))
	case BaseFloat64:
		return math.Float64frombits(u)
	default:
		return u
	}
}

// SetValue encodes v into the field in place. The field keeps its size; a
// scalar v fills element 0 and leaves other elements untouched, a slice fills
// elements from the start. nil writes the invalid value.
func (f *Field) SetValue(v any, bigEndian bool) error {
	bt := f.Def.BaseType
	if bt == BaseString {
		s, ok := v.(string)
		if v != nil && !ok {
			return fmt.Errorf("field %d: string expected, got %T", f.Def.Num, v)
		}

		if len(s)+1 > len(f.Raw) {
			return fmt.Errorf("field %d: string %q does not fit in %d bytes", f.Def.Num, s, len(f.Raw))
		}

		for i := range f.Raw {
			f.Raw[i] = 0
		}

		copy(f.Raw, s)

		return nil
	}

	size := bt.Size()

	switch vv := v.(type) {
	case []any:
		if len(vv)*size > len(f.Raw) {
			return fmt.Errorf("field %d: %d elements do not fit in %d bytes", f.Def.Num, len(vv), len(f.Raw))
		}

		for i, e := range vv {
			if err := encodeScalar(bt, f.Raw[i*size:(i+1)*size], e, bigEndian); err != nil {
				return fmt.Errorf("field %d[%d]: %w", f.Def.Num, i, err)
			}
		}

		return nil
	default:
		if len(f.Raw) < size {
			return fmt.Errorf("field %d: raw too short", f.Def.Num)
		}

		return encodeScalar(bt, f.Raw[:size], v, bigEndian)
	}
}

// SetElement encodes v into element idx of an array field.
func (f *Field) SetElement(idx int, v any, bigEndian bool) error {
	bt := f.Def.BaseType

	size := bt.Size()
	if bt == BaseString || (idx+1)*size > len(f.Raw) {
		return fmt.Errorf("field %d: element %d out of range", f.Def.Num, idx)
	}

	return encodeScalar(bt, f.Raw[idx*size:(idx+1)*size], v, bigEndian)
}

func encodeScalar(bt BaseType, dst []byte, v any, bigEndian bool) error {
	if v == nil {
		copy(dst, bt.InvalidBytes(bigEndian))
		return nil
	}

	switch bt {
	case BaseFloat32, BaseFloat64:
		return encodeFloat(bt, dst, v, bigEndian)
	default:
		bits, err := toBits(v)
		if err != nil {
			return err
		}

		writeUint(dst, bits, bigEndian)

		return nil
	}
}

// toBits is the two's-complement bit pattern of any integer kind, which is
// what a FIT field stores; a uint64 above MaxInt64 stays intact.
func toBits(v any) (uint64, error) {
	if u, ok := unsignedInt(v); ok {
		return u, nil
	}

	iv, err := toInt(v)
	if err != nil {
		return 0, err
	}

	return uint64(iv), nil //nolint:gosec // the bit pattern is the point
}

func encodeFloat(bt BaseType, dst []byte, v any, bigEndian bool) error {
	fv, err := toFloat(v)
	if err != nil {
		return err
	}

	if bt == BaseFloat32 {
		writeUint(dst, uint64(math.Float32bits(float32(fv))), bigEndian)
		return nil
	}

	writeUint(dst, math.Float64bits(fv), bigEndian)

	return nil
}

// toInt converts any Go integer, an integral float or a bool to int64.
func toInt(v any) (int64, error) {
	if i, ok := signedInt(v); ok {
		return i, nil
	}

	if u, ok := unsignedInt(v); ok {
		if u > math.MaxInt64 {
			return 0, fmt.Errorf("integer %d is too large", u)
		}

		return int64(u), nil
	}

	switch x := v.(type) {
	case float32:
		return floatToInt(float64(x))
	case float64:
		return floatToInt(x)
	case bool:
		if x {
			return 1, nil
		}

		return 0, nil
	}

	return 0, fmt.Errorf("integer expected, got %T", v)
}

func signedInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	}

	return 0, false
}

func unsignedInt(v any) (uint64, bool) {
	switch x := v.(type) {
	case uint:
		return uint64(x), true
	case uint8:
		return uint64(x), true
	case uint16:
		return uint64(x), true
	case uint32:
		return uint64(x), true
	case uint64:
		return x, true
	}

	return 0, false
}

func floatToInt(x float64) (int64, error) {
	if x != math.Trunc(x) {
		return 0, fmt.Errorf("integer expected, got %v", x)
	}

	return int64(x), nil
}

func toFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	}

	i, err := toInt(v)
	if err != nil {
		return 0, err
	}

	return float64(i), nil
}

var (
	le = binary.LittleEndian
	be = binary.BigEndian
)
