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

	end := hdrSize + dataSize
	if end > len(data) {
		return nil, fmt.Errorf("fit: data size %d exceeds file size %d", dataSize, len(data))
	}

	crcErr := checkCRCs(data, hdrSize, end)
	defs := map[uint8]*Definition{}

	p := hdrSize
	for p < end {
		n, err := decodeRecord(f, defs, data[p:end])
		if err != nil {
			return f, fmt.Errorf("fit: record at %d: %w", p, err)
		}

		p += n
	}

	return f, crcErr
}

// checkCRCs verifies the header CRC (14-byte headers only) and the file CRC
// when present; the first mismatch is returned.
func checkCRCs(data []byte, hdrSize, end int) error {
	if hdrSize == 14 {
		want := le.Uint16(data[12:14])
		if want != 0 && CRC(data[:12]) != want {
			return errors.New("fit: header CRC mismatch")
		}
	}

	if end+2 <= len(data) && CRC(data[:end]) != le.Uint16(data[end:end+2]) {
		return errors.New("fit: file CRC mismatch")
	}

	return nil
}

// decodeRecord parses the record at the start of b (header byte included),
// appends it to f and returns its length in bytes.
func decodeRecord(f *File, defs map[uint8]*Definition, b []byte) (int, error) {
	h := b[0]
	b = b[1:]

	if h&0x80 != 0 { // compressed timestamp header
		msg, n, err := decodeDataFor(defs, (h>>5)&0x03, b)
		if err != nil {
			return 0, err
		}

		msg.Compressed = true
		msg.TimeOffset = h & 0x1F
		f.Records = append(f.Records, Record{Kind: RecData, Msg: msg})

		return 1 + n, nil
	}

	local := h & 0x0F
	if h&0x40 != 0 {
		def, n, err := decodeDefinition(local, h&0x20 != 0, b)
		if err != nil {
			return 0, fmt.Errorf("definition: %w", err)
		}

		defs[local] = def
		f.Records = append(f.Records, Record{Kind: RecDefinition, Def: def})

		return 1 + n, nil
	}

	msg, n, err := decodeDataFor(defs, local, b)
	if err != nil {
		return 0, err
	}

	f.Records = append(f.Records, Record{Kind: RecData, Msg: msg})

	return 1 + n, nil
}

func decodeDataFor(defs map[uint8]*Definition, local uint8, b []byte) (*Message, int, error) {
	def, ok := defs[local]
	if !ok {
		return nil, 0, fmt.Errorf("data record references undefined local message %d", local)
	}

	return decodeData(def, b)
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

	for range n {
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

		for range nd {
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
