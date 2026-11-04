package testdump

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// MusicTrack is an existing audio path in the synthetic media tree.
const MusicTrack = "Music/Artist/Album/Local.mp3"

// MusicPayload contains only synthetic ID3v1 fields, not playable audio.
func MusicPayload() []byte {
	data := make([]byte, 128)
	copy(data, "TAG")
	copy(data[3:33], "Fixture Song")
	copy(data[33:63], "Artist/Name")
	copy(data[63:93], "Album")

	return data
}

// MusicRoot adds deterministic media metadata and a relative-path playlist.
func MusicRoot(tb testing.TB) string {
	tb.Helper()
	root := Root(tb)
	AddMusic(tb, root)

	return root
}

// AddMusic adds fixed synthetic media to an existing temporary fixture root.
func AddMusic(tb testing.TB, root string) {
	tb.Helper()

	files := map[string][]byte{
		MusicTrack:                []byte("synthetic local audio"),
		"Podcasts/episode.aac":    []byte("synthetic podcast"),
		"Audiobooks/chapter.flac": []byte("synthetic audiobook"),
		"Music/ignored.txt":       []byte("not an audio file"),
		"Music/Fixture.m3u":       []byte("#EXTM3U\nArtist\\Album\\Local.mp3\nPodcasts/episode.aac\n../Audiobooks/chapter.flac\n"),
	}
	stamp := time.Date(2000, time.January, 2, 3, 4, 5, 0, time.UTC)

	for name, data := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			tb.Fatal(err)
		}

		if err := os.WriteFile(file, data, 0o600); err != nil {
			tb.Fatal(err)
		}

		if err := os.Chtimes(file, stamp, stamp); err != nil {
			tb.Fatal(err)
		}
	}
}
