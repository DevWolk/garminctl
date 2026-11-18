package fit

// Encode preserves record order and field bytes, and writes a 14-byte header
// with recalculated header and file CRCs.
func (f *File) Encode() []byte {
	body := make([]byte, 0, 1024)

	for _, r := range f.Records {
		switch r.Kind {
		case RecDefinition:
			body = appendDefinition(body, r.Def)
		case RecData:
			body = appendData(body, r.Msg)
		}
	}

	out := make([]byte, 14, 14+len(body)+2)
	out[0] = 14
	out[1] = f.ProtocolVersion
	le.PutUint16(out[2:4], f.ProfileVersion)
	le.PutUint32(out[4:8], uint32(len(body))) //nolint:gosec // the header stores the data size as uint32; Decode and Builder never produce more
	copy(out[8:12], ".FIT")
	le.PutUint16(out[12:14], CRC(out[:12]))
	out = append(out, body...)
	crc := CRC(out)
	out = append(out, byte(crc), byte(crc>>8)) //nolint:gosec // the two CRC bytes, little-endian

	return out
}

func appendDefinition(dst []byte, d *Definition) []byte {
	h := 0x40 | (d.LocalNum & 0x0F)
	if len(d.DevFields) > 0 {
		h |= 0x20
	}

	dst = append(dst, h, 0)
	//nolint:gosec // the two bytes of the global message number in the chosen byte order
	if d.BigEndian {
		dst = append(dst, 1, byte(d.GlobalNum>>8), byte(d.GlobalNum))
	} else {
		dst = append(dst, 0, byte(d.GlobalNum), byte(d.GlobalNum>>8))
	}

	dst = append(dst, byte(len(d.Fields))) //nolint:gosec // Decode reads one byte and Builder.Write refuses more than 255
	for _, fd := range d.Fields {
		dst = append(dst, fd.Num, fd.Size, byte(fd.BaseType))
	}

	if len(d.DevFields) > 0 {
		dst = append(dst, byte(len(d.DevFields))) //nolint:gosec // developer fields come from Decode, which reads a one-byte count
		for _, fd := range d.DevFields {
			dst = append(dst, fd.Num, fd.Size, fd.DevIndex)
		}
	}

	return dst
}

func appendData(dst []byte, m *Message) []byte {
	if m.Compressed {
		dst = append(dst, 0x80|((m.Def.LocalNum&0x03)<<5)|(m.TimeOffset&0x1F))
	} else {
		dst = append(dst, m.Def.LocalNum&0x0F)
	}

	for _, f := range m.Fields {
		dst = append(dst, f.Raw...)
	}

	for _, f := range m.DevFields {
		dst = append(dst, f.Raw...)
	}

	return dst
}
