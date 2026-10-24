package mtp

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"

	"github.com/DevWolk/garminctl/internal/device"
)

const folderFormat = 0x3001

type object struct {
	handle uint32
	parent uint32
	entry  device.Entry
}

func stringAt(data []byte, offset int) (string, error) {
	if offset >= len(data) {
		return "", errors.New("short MTP string")
	}

	count := int(data[offset])
	if offset+1+count*2 > len(data) {
		return "", errors.New("truncated MTP string")
	}

	units := make([]uint16, count)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[offset+1+i*2:])
	}

	return strings.TrimRight(string(utf16.Decode(units)), "\x00"), nil
}

func parseObject(handle uint32, data []byte) (object, error) {
	if len(data) < 53 {
		return object{}, errors.New("short MTP ObjectInfo")
	}

	name, err := stringAt(data, 52)
	if err != nil {
		return object{}, err
	}

	return object{handle: handle, parent: binary.LittleEndian.Uint32(data[38:]), entry: device.Entry{
		Name: name, Dir: binary.LittleEndian.Uint16(data[4:]) == folderFormat, Size: int64(binary.LittleEndian.Uint32(data[8:])),
	}}, nil
}

func encodeObject(storage, parent uint32, name string, size int, dir bool) ([]byte, error) {
	units := utf16.Encode([]rune(name))
	if len(units) > 254 || !validName(name) {
		return nil, errors.New("invalid MTP file name")
	}

	if size < 0 || size > maxContainer-12 {
		return nil, errors.New("MTP file exceeds 512 MiB")
	}

	data := make([]byte, 52)
	binary.LittleEndian.PutUint32(data, storage)
	binary.LittleEndian.PutUint16(data[4:], 0x3000)
	binary.LittleEndian.PutUint16(data[12:], 0x3000) // Garmin expects undefined thumbnail format too.
	binary.LittleEndian.PutUint32(data[8:], uint32(size))
	binary.LittleEndian.PutUint32(data[38:], parent)

	if dir {
		binary.LittleEndian.PutUint16(data[4:], folderFormat)
		binary.LittleEndian.PutUint16(data[42:], 1) // GenericFolder association.
	}

	data = append(data, byte(len(units)+1)) //nolint:gosec // at most 254 UTF-16 units above.
	for _, unit := range units {
		data = binary.LittleEndian.AppendUint16(data, unit)
	}

	data = append(data, 0, 0, 0, 0, 0) // NUL and empty creation/modification/keyword strings.

	return data, nil
}

func uint32Array(data []byte) ([]uint32, error) {
	if len(data) < 4 {
		return nil, errors.New("short MTP array")
	}

	n := binary.LittleEndian.Uint32(data)
	if uint64(n)*4 != uint64(len(data))-4 {
		return nil, errors.New("invalid MTP array length")
	}

	out := make([]uint32, int(n))
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(data[4+i*4:])
	}

	return out, nil
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

func segments(path string) ([]string, error) {
	path = strings.Trim(path, "/")
	if path == "" || path == "." {
		return nil, nil
	}

	parts := strings.Split(path, "/")
	for _, part := range parts {
		if !validName(part) {
			return nil, errors.New("invalid watch path")
		}
	}

	return parts, nil
}
