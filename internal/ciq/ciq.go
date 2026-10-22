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
		return int32(v), err
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
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		arr := make([]any, 0, n)
		for i := uint32(0); i < n; i++ {
			v, err := d.value()
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case TypeDict:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		dict := make(Dict, 0, n)
		for i := uint32(0); i < n; i++ {
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
	case TypeLong:
		if err := d.need(8); err != nil {
			return nil, err
		}
		v := int64(binary.BigEndian.Uint64(d.data[d.pos:]))
		d.pos += 8
		return v, nil
	case TypeDouble:
		if err := d.need(8); err != nil {
			return nil, err
		}
		v := math.Float64frombits(binary.BigEndian.Uint64(d.data[d.pos:]))
		d.pos += 8
		return v, nil
	}
	return nil, fmt.Errorf("ciq: unsupported type 0x%02X at %d", t, d.pos-1)
}

// Encode serialises v. Strings are interned in the string block in order
// of first appearance, which reproduces the files the watch writes.
func Encode(v any) ([]byte, error) {
	e := &encoder{strIndex: map[string]uint32{}}
	if err := e.value(v); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 16+len(e.strs)+len(e.data))
	if len(e.strs) > 0 {
		out = binary.BigEndian.AppendUint32(out, stringSentinel)
		out = binary.BigEndian.AppendUint32(out, uint32(len(e.strs)))
		out = append(out, e.strs...)
	}
	out = binary.BigEndian.AppendUint32(out, dataSentinel)
	out = binary.BigEndian.AppendUint32(out, uint32(len(e.data)))
	out = append(out, e.data...)
	return out, nil
}

type encoder struct {
	strs     []byte
	data     []byte
	strIndex map[string]uint32
}

func (e *encoder) intern(s string) uint32 {
	if off, ok := e.strIndex[s]; ok {
		return off
	}
	off := uint32(len(e.strs))
	e.strIndex[s] = off
	e.strs = binary.BigEndian.AppendUint16(e.strs, uint16(len(s)+1))
	e.strs = append(e.strs, s...)
	e.strs = append(e.strs, 0)
	return off
}

func (e *encoder) value(v any) error {
	switch x := v.(type) {
	case nil:
		e.data = append(e.data, TypeNull)
	case int32:
		e.data = append(e.data, TypeInt)
		e.data = binary.BigEndian.AppendUint32(e.data, uint32(x))
	case int:
		e.data = append(e.data, TypeInt)
		e.data = binary.BigEndian.AppendUint32(e.data, uint32(int32(x)))
	case int64:
		e.data = append(e.data, TypeLong)
		e.data = binary.BigEndian.AppendUint64(e.data, uint64(x))
	case float32:
		e.data = append(e.data, TypeFloat)
		e.data = binary.BigEndian.AppendUint32(e.data, math.Float32bits(x))
	case float64:
		e.data = append(e.data, TypeDouble)
		e.data = binary.BigEndian.AppendUint64(e.data, math.Float64bits(x))
	case string:
		e.data = append(e.data, TypeString)
		e.data = binary.BigEndian.AppendUint32(e.data, e.intern(x))
	case bool:
		b := byte(0)
		if x {
			b = 1
		}
		e.data = append(e.data, TypeBool, b)
	case []any:
		e.data = append(e.data, TypeArray)
		e.data = binary.BigEndian.AppendUint32(e.data, uint32(len(x)))
		for _, el := range x {
			if err := e.value(el); err != nil {
				return err
			}
		}
	case Dict:
		e.data = append(e.data, TypeDict)
		e.data = binary.BigEndian.AppendUint32(e.data, uint32(len(x)))
		for _, p := range x {
			if err := e.value(p.Key); err != nil {
				return err
			}
			if err := e.value(p.Value); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("ciq: cannot encode %T", v)
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
	case int32:
		switch n := newValue.(type) {
		case float64:
			if n != math.Trunc(n) {
				return nil, fmt.Errorf("integer expected, got %v", n)
			}
			return int32(n), nil
		case int:
			return int32(n), nil
		case int32:
			return n, nil
		case int64:
			return int32(n), nil
		case bool:
			if n {
				return int32(1), nil
			}
			return int32(0), nil
		case string:
			var i int64
			if _, err := fmt.Sscan(n, &i); err != nil {
				return nil, fmt.Errorf("integer expected, got %q", n)
			}
			return int32(i), nil
		}
	case int64:
		switch n := newValue.(type) {
		case float64:
			return int64(n), nil
		case int:
			return int64(n), nil
		case int64:
			return n, nil
		}
	case float32:
		switch n := newValue.(type) {
		case float64:
			return float32(n), nil
		case int:
			return float32(n), nil
		case float32:
			return n, nil
		case string:
			var f float64
			if _, err := fmt.Sscan(n, &f); err != nil {
				return nil, fmt.Errorf("number expected, got %q", n)
			}
			return float32(f), nil
		}
	case float64:
		switch n := newValue.(type) {
		case float64:
			return n, nil
		case int:
			return float64(n), nil
		}
	case bool:
		switch n := newValue.(type) {
		case bool:
			return n, nil
		case string:
			switch n {
			case "true", "1", "on":
				return true, nil
			case "false", "0", "off":
				return false, nil
			}
		case float64:
			return n != 0, nil
		}
	case string:
		switch n := newValue.(type) {
		case string:
			return n, nil
		default:
			return fmt.Sprint(n), nil
		}
	case nil:
		return newValue, nil
	}
	return nil, fmt.Errorf("cannot convert %T to %T", newValue, existing)
}
