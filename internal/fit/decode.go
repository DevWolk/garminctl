package fit

import (
	"errors"
	"fmt"
)

// ErrNotFIT is returned when the data does not start with a FIT header.
var ErrNotFIT = errors.New("fit: not a FIT file")

// IsFIT reports whether data looks like a FIT file.
func IsFIT(data []byte) bool {
	return len(data) >= 12 && (data[0] == 12 || data[0] == 14) && string(data[8:12]) == ".FIT"
}

// Decode parses a FIT file. CRC mismatches are reported as errors; the
// decoded file is still returned so callers can inspect damaged files.
func Decode(data []byte) (*File, error) {
	if !IsFIT(data) {
		return nil, ErrNotFIT
	}
	hdrSize := int(data[0])
	f := &File{ProtocolVersion: data[1], ProfileVersion: le.Uint16(data[2:4])}
	dataSize := int(le.Uint32(data[4:8]))
	var crcErr error
	if hdrSize == 14 {
		want := le.Uint16(data[12:14])
		if want != 0 && CRC(data[:12]) != want {
			crcErr = errors.New("fit: header CRC mismatch")
		}
	}
	end := hdrSize + dataSize
	if end > len(data) {
		return nil, fmt.Errorf("fit: data size %d exceeds file size %d", dataSize, len(data))
	}
	if end+2 <= len(data) {
		want := le.Uint16(data[end : end+2])
		if CRC(data[:end]) != want && crcErr == nil {
			crcErr = errors.New("fit: file CRC mismatch")
		}
	}
	defs := map[uint8]*Definition{}
	p := hdrSize
	for p < end {
		h := data[p]
		p++
		if h&0x80 != 0 {
			local := (h >> 5) & 0x03
			def, ok := defs[local]
			if !ok {
				return f, fmt.Errorf("fit: data record at %d references undefined local message %d", p-1, local)
			}
			msg, n, err := decodeData(def, data[p:end])
			if err != nil {
				return f, fmt.Errorf("fit: record at %d: %w", p-1, err)
			}
			msg.Compressed = true
			msg.TimeOffset = h & 0x1F
			f.Records = append(f.Records, Record{Kind: RecData, Msg: msg})
			p += n
			continue
		}
		local := h & 0x0F
		if h&0x40 != 0 {
			def, n, err := decodeDefinition(local, h&0x20 != 0, data[p:end])
			if err != nil {
				return f, fmt.Errorf("fit: definition at %d: %w", p-1, err)
			}
			defs[local] = def
			f.Records = append(f.Records, Record{Kind: RecDefinition, Def: def})
			p += n
			continue
		}
		def, ok := defs[local]
		if !ok {
			return f, fmt.Errorf("fit: data record at %d references undefined local message %d", p-1, local)
		}
		msg, n, err := decodeData(def, data[p:end])
		if err != nil {
			return f, fmt.Errorf("fit: record at %d: %w", p-1, err)
		}
		f.Records = append(f.Records, Record{Kind: RecData, Msg: msg})
		p += n
	}
	return f, crcErr
}

func decodeDefinition(local uint8, hasDev bool, b []byte) (*Definition, int, error) {
	if len(b) < 5 {
		return nil, 0, errors.New("truncated")
	}
	d := &Definition{LocalNum: local, BigEndian: b[1] == 1}
	if d.BigEndian {
		d.GlobalNum = be.Uint16(b[2:4])
	} else {
		d.GlobalNum = le.Uint16(b[2:4])
	}
	n := int(b[4])
	p := 5
	if len(b) < p+3*n {
		return nil, 0, errors.New("truncated fields")
	}
	for i := 0; i < n; i++ {
		d.Fields = append(d.Fields, FieldDef{Num: b[p], Size: b[p+1], BaseType: BaseType(b[p+2])})
		p += 3
	}
	if hasDev {
		if len(b) < p+1 {
			return nil, 0, errors.New("truncated developer fields")
		}
		nd := int(b[p])
		p++
		if len(b) < p+3*nd {
			return nil, 0, errors.New("truncated developer fields")
		}
		for i := 0; i < nd; i++ {
			d.DevFields = append(d.DevFields, DevFieldDef{Num: b[p], Size: b[p+1], DevIndex: b[p+2]})
			p += 3
		}
	}
	return d, p, nil
}

func decodeData(def *Definition, b []byte) (*Message, int, error) {
	size := def.DataSize()
	if len(b) < size {
		return nil, 0, errors.New("truncated data record")
	}
	m := &Message{Def: def}
	p := 0
	for _, fd := range def.Fields {
		raw := make([]byte, fd.Size)
		copy(raw, b[p:p+int(fd.Size)])
		m.Fields = append(m.Fields, Field{Def: fd, Raw: raw})
		p += int(fd.Size)
	}
	for _, fd := range def.DevFields {
		raw := make([]byte, fd.Size)
		copy(raw, b[p:p+int(fd.Size)])
		m.DevFields = append(m.DevFields, DevField{Def: fd, Raw: raw})
		p += int(fd.Size)
	}
	return m, size, nil
}
