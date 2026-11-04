package music

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// Tags are the ID3 fields used for the folder layout.
type Tags struct{ Title, Artist, Album string }

func decodeID3Text(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	enc, data := b[0], b[1:]

	var s string

	switch enc {
	case 0:
		r := make([]rune, len(data))
		for i, c := range data {
			r[i] = rune(c)
		}

		s = string(r)
	case 1, 2:
		if len(data) >= 2 && ((data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF)) {
			le := data[0] == 0xFF
			data = data[2:]

			u := make([]uint16, 0, len(data)/2)
			for i := 0; i+1 < len(data); i += 2 {
				if le {
					u = append(u, binary.LittleEndian.Uint16(data[i:]))
				} else {
					u = append(u, binary.BigEndian.Uint16(data[i:]))
				}
			}

			s = string(utf16.Decode(u))
		} else {
			u := make([]uint16, 0, len(data)/2)
			for i := 0; i+1 < len(data); i += 2 {
				u = append(u, binary.BigEndian.Uint16(data[i:]))
			}

			s = string(utf16.Decode(u))
		}
	default:
		s = string(data)
	}

	return strings.TrimRight(s, "\x00")
}

// ReadTags reads ID3v2 (2.3/2.4) or ID3v1 title/artist/album.
func ReadTags(data []byte) Tags {
	var t Tags

	if len(data) > 10 && string(data[:3]) == "ID3" {
		readID3v2(data, &t)
	}

	if (t.Title == "" || t.Artist == "") && len(data) >= 128 && string(data[len(data)-128:len(data)-125]) == "TAG" {
		readID3v1(data[len(data)-128:], &t)
	}

	return t
}

// syncsafe decodes a 4-byte ID3 syncsafe integer (7 bits per byte).
func syncsafe(b []byte) int {
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

// id3Size reads a frame or extended-header size: syncsafe in v2.4, plain
// big-endian in v2.3.
func id3Size(ver byte, b []byte) int {
	if ver == 4 {
		return syncsafe(b)
	}

	return int(binary.BigEndian.Uint32(b))
}

// readID3v2 walks the frames of an ID3v2 header into t.
func readID3v2(data []byte, t *Tags) {
	ver := data[3]
	end := min(10+syncsafe(data[6:10]), len(data))

	pos := 10
	if data[5]&0x40 != 0 && pos+4 <= end { // extended header
		pos += id3Size(ver, data[pos:pos+4])
		if ver != 4 {
			pos += 4 // v2.3 sizes exclude the size field itself
		}
	}

	for pos+10 <= end {
		id := string(data[pos : pos+4])
		if id[0] == 0 {
			break
		}

		fsz := id3Size(ver, data[pos+4:pos+8])

		pos += 10
		if fsz < 0 || pos+fsz > end {
			break
		}

		setTag(t, id, decodeID3Text(data[pos:pos+fsz]))

		pos += fsz
	}
}

func setTag(t *Tags, id, text string) {
	switch id {
	case "TIT2":
		t.Title = text
	case "TPE1":
		t.Artist = text
	case "TALB":
		t.Album = text
	}
}

// readID3v1 fills what is still missing from a 128-byte trailing TAG block.
func readID3v1(tag []byte, t *Tags) {
	clean := func(b []byte) string {
		return strings.TrimSpace(strings.TrimRight(string(bytes.TrimRight(b, "\x00")), " "))
	}

	if t.Title == "" {
		t.Title = clean(tag[3:33])
	}

	if t.Artist == "" {
		t.Artist = clean(tag[33:63])
	}

	if t.Album == "" {
		t.Album = clean(tag[63:93])
	}
}
