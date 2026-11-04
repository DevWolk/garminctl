package music

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestReadTagsID3v2(t *testing.T) {
	t.Parallel()

	frame := func(id, text string) []byte {
		body := append([]byte{0}, []byte(text)...)

		var size [4]byte

		binary.BigEndian.PutUint32(size[:], uint32(len(body))) //nolint:gosec // test frames are a few bytes long

		b := []byte(id)
		b = append(b, size[:]...)
		b = append(b, 0, 0)

		return append(b, body...)
	}
	frames := slices.Concat(frame("TIT2", "Song"), frame("TPE1", "Artist/Name"), frame("TALB", "Album"))
	size := len(frames)
	hdr := []byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}
	data := slices.Concat(hdr, frames)

	tags := ReadTags(data)
	if tags.Title != "Song" || tags.Artist != "Artist/Name" || tags.Album != "Album" {
		t.Fatalf("tags: %+v", tags)
	}

	if safeName(tags.Artist, "x") != "Artist Name" {
		t.Fatalf("safeName: %q", safeName(tags.Artist, "x"))
	}
}
