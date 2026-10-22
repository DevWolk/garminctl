// Package ciq implements Garmin's Connect IQ serialization format, used by
// the .SET files that hold Connect IQ app settings on the watch and by the
// SETTINGS section of .PRG files.
//
// Layout: an optional string block (sentinel 0xABCDABCD, uint32 length,
// then entries of uint16 length + UTF-8 bytes + NUL) followed by a data
// block (sentinel 0xDA7ADA7A, uint32 length, one typed value). Everything is
// big-endian. Strings in the data block are uint32 offsets into the string
// block.
package ciq

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	stringSentinel = 0xABCDABCD
	dataSentinel   = 0xDA7ADA7A
)

// Type codes of serialized values.
const (
	TypeNull   = 0x00
	TypeInt    = 0x01
	TypeFloat  = 0x02
	TypeString = 0x03
	TypeArray  = 0x05
	TypeBool   = 0x09
	TypeDict   = 0x0B
	TypeLong   = 0x0E
	TypeDouble = 0x0F
)

// Pair is one dictionary entry. Dictionaries keep insertion order.
type Pair struct {
	Key   any
	Value any
}

// Dict is an ordered dictionary.
type Dict []Pair

// Get returns the value stored under key.
//
// It takes a value receiver on purpose: it is a read-only lookup that must
// work on a Dict pulled out of an interface, while Set has to append.
func (d Dict) Get(key any) (any, bool) {
	for _, p := range d {
		if p.Key == key {
			return p.Value, true
		}
	}

	return nil, false
}

// Set replaces the value under key or appends a new pair.
func (d *Dict) Set(key, value any) {
	for i := range *d {
		if (*d)[i].Key == key {
			(*d)[i].Value = value
			return
		}
	}

	*d = append(*d, Pair{Key: key, Value: value})
}

// Decode parses a serialized value. Ints decode to int32, floats to
// float32, longs to int64, doubles to float64, strings to string, bools to
// bool, arrays to []any and dictionaries to Dict.
func Decode(b []byte) (any, error) {
	var strs []byte

	p := 0

	if len(b) >= 8 && binary.BigEndian.Uint32(b) == stringSentinel {
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if 8+n > len(b) {
			return nil, errors.New("ciq: string block truncated")
		}

		strs = b[8 : 8+n]
		p = 8 + n
	}

	if len(b) < p+8 || binary.BigEndian.Uint32(b[p:]) != dataSentinel {
		return nil, errors.New("ciq: data block sentinel missing")
	}

	n := int(binary.BigEndian.Uint32(b[p+4:]))
	if p+8+n > len(b) {
		return nil, errors.New("ciq: data block truncated")
	}

	d := &decoder{data: b[p+8 : p+8+n], strs: strs}

	v, err := d.value()
	if err != nil {
		return nil, err
	}

	if d.pos != len(d.data) {
		return nil, fmt.Errorf("ciq: %d trailing bytes in data block", len(d.data)-d.pos)
	}

	return v, nil
}

type decoder struct {
	data []byte
	strs []byte
	pos  int
}

func (d *decoder) need(n int) error {
	if d.pos+n > len(d.data) {
		return errors.New("ciq: unexpected end of data")
	}

	return nil
}

func (d *decoder) u32() (uint32, error) {
	if err := d.need(4); err != nil {
		return 0, err
	}

	v := binary.BigEndian.Uint32(d.data[d.pos:])
	d.pos += 4

	return v, nil
}

func (d *decoder) u64() (uint64, error) {
	if err := d.need(8); err != nil {
		return 0, err
	}

	v := binary.BigEndian.Uint64(d.data[d.pos:])
	d.pos += 8

	return v, nil
}

func (d *decoder) str(off uint32) (string, error) {
	if int(off)+2 > len(d.strs) {
		return "", fmt.Errorf("ciq: string offset %d out of range", off)
	}

	l := int(binary.BigEndian.Uint16(d.strs[off:]))

	start := int(off) + 2
	if start+l > len(d.strs) || l == 0 {
		return "", fmt.Errorf("ciq: string at %d truncated", off)
	}

	return string(d.strs[start : start+l-1]), nil
}

func (d *decoder) value() (any, error) {
	if err := d.need(1); err != nil {
		return nil, err
	}

	t := d.data[d.pos]
	d.pos++

	switch t {
	case TypeNull:
		return nil, nil
	case TypeInt:
		v, err := d.u32()
		return int32(v), err //nolint:gosec // the 4-byte field holds a two's-complement int32
	case TypeFloat:
		v, err := d.u32()
		return math.Float32frombits(v), err
	case TypeString:
		off, err := d.u32()
		if err != nil {
			return nil, err
		}

		return d.str(off)
	case TypeBool:
		if err := d.need(1); err != nil {
			return nil, err
		}

		v := d.data[d.pos] != 0
		d.pos++

		return v, nil
	case TypeArray:
		return d.array()
	case TypeDict:
		return d.dict()
	case TypeLong:
		v, err := d.u64()
		return int64(v), err //nolint:gosec // the 8-byte field holds a two's-complement int64
	case TypeDouble:
		v, err := d.u64()
		return math.Float64frombits(v), err
	}

	return nil, fmt.Errorf("ciq: unsupported type 0x%02X at %d", t, d.pos-1)
}

// remaining bounds a count read from the file: every element takes at least
// one byte, so a larger count is corrupt and must not drive an allocation.
func (d *decoder) remaining(n uint32) int {
	return min(int(n), len(d.data)-d.pos)
}

func (d *decoder) array() ([]any, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}

	arr := make([]any, 0, d.remaining(n))

	for range n {
		v, err := d.value()
		if err != nil {
			return nil, err
		}

		arr = append(arr, v)
	}

	return arr, nil
}

func (d *decoder) dict() (Dict, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}

	dict := make(Dict, 0, d.remaining(n))

	for range n {
		k, err := d.value()
		if err != nil {
			return nil, err
		}

		v, err := d.value()
		if err != nil {
			return nil, err
		}

		dict = append(dict, Pair{Key: k, Value: v})
	}

	return dict, nil
}

// Encode serialises v. Strings are interned in the string block in order
// of first appearance, which reproduces the files the watch writes.
func Encode(v any) ([]byte, error) {
	e := &encoder{strIndex: map[string]uint32{}}
	if err := e.value(v); err != nil {
		return nil, err
	}

	strLen, err := u32Len(len(e.strs), "string block")
	if err != nil {
		return nil, err
	}

	dataLen, err := u32Len(len(e.data), "data block")
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 16+len(e.strs)+len(e.data))
	if len(e.strs) > 0 {
		out = binary.BigEndian.AppendUint32(out, stringSentinel)
		out = binary.BigEndian.AppendUint32(out, strLen)
		out = append(out, e.strs...)
	}

	out = binary.BigEndian.AppendUint32(out, dataSentinel)
	out = binary.BigEndian.AppendUint32(out, dataLen)
	out = append(out, e.data...)

	return out, nil
}

// u32Len checks that a length or element count fits the format's uint32.
func u32Len(n int, what string) (uint32, error) {
	if n < 0 || int64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("ciq: %s of %d does not fit in 32 bits", what, n)
	}

	return uint32(n), nil
}

type encoder struct {
	strs     []byte
	data     []byte
	strIndex map[string]uint32
}

func (e *encoder) intern(s string) (uint32, error) {
	if off, ok := e.strIndex[s]; ok {
		return off, nil
	}

	// The uint16 length prefix counts the NUL terminator.
	n := len(s) + 1
	if n > math.MaxUint16 {
		return 0, fmt.Errorf("ciq: a %d-byte string is too long for the string block", len(s))
	}

	off, err := u32Len(len(e.strs), "string block")
	if err != nil {
		return 0, err
	}

	e.strIndex[s] = off
	e.strs = binary.BigEndian.AppendUint16(e.strs, uint16(n))
	e.strs = append(e.strs, s...)
	e.strs = append(e.strs, 0)

	return off, nil
}

func (e *encoder) value(v any) error {
	switch x := v.(type) {
	case nil:
		e.data = append(e.data, TypeNull)
	case int32:
		e.putInt32(x)
	case int:
		if x < math.MinInt32 || x > math.MaxInt32 {
			return fmt.Errorf("ciq: %d does not fit a 32-bit int", x)
		}

		e.putInt32(int32(x))
	case int64:
		e.data = append(e.data, TypeLong)
		e.data = binary.BigEndian.AppendUint64(e.data, uint64(x)) //nolint:gosec // two's-complement bits of the int64
	case float32:
		e.data = append(e.data, TypeFloat)
		e.data = binary.BigEndian.AppendUint32(e.data, math.Float32bits(x))
	case float64:
		e.data = append(e.data, TypeDouble)
		e.data = binary.BigEndian.AppendUint64(e.data, math.Float64bits(x))
	case string:
		return e.putString(x)
	case bool:
		e.putBool(x)
	case []any:
		return e.putArray(x)
	case Dict:
		return e.putDict(x)
	default:
		return fmt.Errorf("ciq: cannot encode %T", v)
	}

	return nil
}

func (e *encoder) putInt32(x int32) {
	e.data = append(e.data, TypeInt)
	e.data = binary.BigEndian.AppendUint32(e.data, uint32(x)) //nolint:gosec // two's-complement bits of the int32
}

func (e *encoder) putBool(x bool) {
	b := byte(0)
	if x {
		b = 1
	}

	e.data = append(e.data, TypeBool, b)
}

func (e *encoder) putString(x string) error {
	off, err := e.intern(x)
	if err != nil {
		return err
	}

	e.data = append(e.data, TypeString)
	e.data = binary.BigEndian.AppendUint32(e.data, off)

	return nil
}

func (e *encoder) putArray(x []any) error {
	n, err := u32Len(len(x), "array length")
	if err != nil {
		return err
	}

	e.data = append(e.data, TypeArray)
	e.data = binary.BigEndian.AppendUint32(e.data, n)

	for _, el := range x {
		if err := e.value(el); err != nil {
			return err
		}
	}

	return nil
}

func (e *encoder) putDict(x Dict) error {
	n, err := u32Len(len(x), "dictionary length")
	if err != nil {
		return err
	}

	e.data = append(e.data, TypeDict)
	e.data = binary.BigEndian.AppendUint32(e.data, n)

	for _, p := range x {
		if err := e.value(p.Key); err != nil {
			return err
		}

		if err := e.value(p.Value); err != nil {
			return err
		}
	}

	return nil
}

// ToJSONValue converts a decoded value into plain Go types suitable for
// encoding/json: Dict becomes map[string]any (key order is lost).
func ToJSONValue(v any) any {
	switch x := v.(type) {
	case Dict:
		m := make(map[string]any, len(x))
		for _, p := range x {
			m[fmt.Sprint(p.Key)] = ToJSONValue(p.Value)
		}

		return m
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = ToJSONValue(el)
		}

		return out
	}

	return v
}

// Coerce converts a JSON-ish value (float64, string, bool) into the type
// of an existing setting value so that edits keep the original type.
func Coerce(existing, newValue any) (any, error) {
	switch existing.(type) {
	case int32, int64:
		return coerceInt(existing, newValue)
	case float32, float64:
		return coerceFloat(existing, newValue)
	case bool:
		b, ok := toBool(newValue)
		if !ok {
			return nil, fmt.Errorf("true or false expected, got %v", newValue)
		}

		return b, nil
	case string:
		if s, ok := newValue.(string); ok {
			return s, nil
		}

		return fmt.Sprint(newValue), nil
	case nil:
		return newValue, nil
	}

	return nil, fmt.Errorf("cannot convert %T to %T", newValue, existing)
}

func coerceInt(existing, newValue any) (any, error) {
	i, ok := toInt64(newValue)
	if !ok {
		return nil, fmt.Errorf("integer expected, got %v", newValue)
	}

	if _, is32 := existing.(int32); !is32 {
		return i, nil
	}

	if i < math.MinInt32 || i > math.MaxInt32 {
		return nil, fmt.Errorf("%d does not fit a 32-bit setting", i)
	}

	return int32(i), nil
}

func coerceFloat(existing, newValue any) (any, error) {
	f, ok := toFloat64(newValue)
	if !ok {
		return nil, fmt.Errorf("number expected, got %v", newValue)
	}

	if _, is32 := existing.(float32); is32 {
		return float32(f), nil
	}

	return f, nil
}

// toInt64 accepts integers, integral floats, bools and decimal strings.
func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		if x != math.Trunc(x) {
			return 0, false
		}

		return int64(x), true
	case bool:
		if x {
			return 1, true
		}

		return 0, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return i, err == nil
	}

	return 0, false
}

// toFloat64 accepts floats, decimal strings and whatever toInt64 accepts.
func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}

	i, ok := toInt64(v)

	return float64(i), ok
}

// toBool accepts bools, the words true/false, on/off, 1/0 and integers.
func toBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "on":
			return true, true
		case "false", "0", "off":
			return false, true
		}

		return false, false
	}

	i, ok := toInt64(v)

	return i != 0, ok
}
