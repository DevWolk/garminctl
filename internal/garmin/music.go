package garmin

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/DevWolk/garminctl/internal/device"
)

var audioExt = map[string]bool{".mp3": true, ".m4a": true, ".aac": true, ".adts": true, ".wav": true, ".flac": true}

// MusicFolders are the media folders the watch declares.
var MusicFolders = []string{"Music", "Podcasts", "Audiobooks"}

// Track is one audio file on the watch.
type Track struct {
	Path     string `json:"path"`
	Folder   string `json:"folder"`
	Artist   string `json:"artist,omitempty"`
	Album    string `json:"album,omitempty"`
	Title    string `json:"title"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
}

// Playlist is an .m3u file in Music.
type Playlist struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Tracks []string `json:"tracks"` // watch paths
}

// MusicLibrary is everything audio on the watch.
type MusicLibrary struct {
	Tracks    []Track         `json:"tracks"`
	Playlists []Playlist      `json:"playlists"`
	Bytes     int64           `json:"bytes"`
	Providers []AudioProvider `json:"providers"` // streaming apps whose downloads are hidden from USB
}

// AudioProvider is a Connect IQ audio content provider app (YouTube Music,
// Spotify, Deezer, …). Its downloads are DRM-protected and kept in the app's
// own cache inside the hidden app area, so they never appear under Music/;
// the device XML reports only the cache size.
type AudioProvider struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	CacheBytes int64  `json:"cacheBytes"`
}

func (s *Service) walk(ctx context.Context, dir string, fn func(p string, e device.Entry)) error {
	entries, err := s.dev.List(ctx, dir)
	if err != nil {
		if errors.Is(err, device.ErrNotFound) {
			return nil
		}

		return err
	}

	for _, e := range entries {
		p := dir + "/" + e.Name
		if e.Dir {
			if err := s.walk(ctx, p, fn); err != nil {
				return err
			}

			continue
		}

		fn(p, e)
	}

	return nil
}

// Music lists tracks, playlists and streaming providers.
func (s *Service) Music(ctx context.Context) (*MusicLibrary, error) {
	lib := &MusicLibrary{Tracks: []Track{}, Playlists: []Playlist{}, Providers: []AudioProvider{}}

	playlistFiles, err := s.collectTracks(ctx, lib)
	if err != nil {
		return nil, err
	}

	sort.Slice(lib.Tracks, func(i, j int) bool { return lib.Tracks[i].Path < lib.Tracks[j].Path })

	for _, pf := range playlistFiles {
		pl, err := s.readPlaylist(ctx, pf)
		if err != nil {
			continue
		}

		lib.Playlists = append(lib.Playlists, pl)
	}

	lib.Providers = append(lib.Providers, s.audioProviders(ctx)...)

	return lib, nil
}

// collectTracks walks the media folders into lib and returns the playlist
// files it passed.
func (s *Service) collectTracks(ctx context.Context, lib *MusicLibrary) ([]string, error) {
	var playlistFiles []string

	for _, root := range MusicFolders {
		err := s.walk(ctx, root, func(p string, e device.Entry) {
			ext := strings.ToLower(path.Ext(e.Name))
			if ext == ".m3u" || ext == ".m3u8" {
				playlistFiles = append(playlistFiles, p)
				return
			}

			if !audioExt[ext] {
				return
			}

			lib.Tracks = append(lib.Tracks, trackFromEntry(root, p, e))
			lib.Bytes += e.Size
		})
		if err != nil {
			return nil, err
		}
	}

	return playlistFiles, nil
}

// trackFromEntry describes a file by its place in Folder/Artist/Album/.
func trackFromEntry(root, p string, e device.Entry) Track {
	t := Track{Path: p, Folder: root, Size: e.Size, Modified: e.Modified, Title: strings.TrimSuffix(e.Name, path.Ext(e.Name))}

	rel := strings.Split(strings.TrimPrefix(p, root+"/"), "/")
	switch {
	case len(rel) >= 3:
		t.Artist, t.Album = rel[0], rel[1]
	case len(rel) == 2:
		t.Artist = rel[0]
	}

	return t
}

// readPlaylist parses an .m3u file into watch paths.
func (s *Service) readPlaylist(ctx context.Context, pf string) (Playlist, error) {
	data, err := s.dev.Read(ctx, pf)
	if err != nil {
		return Playlist{}, err
	}

	pl := Playlist{Name: strings.TrimSuffix(path.Base(pf), path.Ext(pf)), Path: pf, Tracks: []string{}}
	base := path.Dir(pf)

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\\", "/"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if musicPath(line) != nil {
			line = path.Join(base, line)
		}

		pl.Tracks = append(pl.Tracks, line)
	}

	return pl, nil
}

// audioProviders lists the streaming apps the device XML declares.
func (s *Service) audioProviders(ctx context.Context) []AudioProvider {
	d, err := s.DeviceXML(ctx)
	if err != nil {
		return nil
	}

	var out []AudioProvider

	for _, a := range d.Apps {
		if a.Type == AudioProviderType {
			out = append(out, AudioProvider{Name: a.Name, Version: a.Version, CacheBytes: a.MediaCacheBytes})
		}
	}

	return out
}

var badChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

func safeName(s, fallback string) string {
	s = strings.TrimSpace(badChars.ReplaceAllString(s, " "))

	s = strings.Trim(s, ". ")
	if s == "" {
		return fallback
	}

	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}

	return s
}

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

// AddTrack writes an audio file into Folder/Artist/Album/Title.ext.
func (s *Service) AddTrack(ctx context.Context, folder, filename string, data []byte) (*Track, error) {
	ext := strings.ToLower(path.Ext(filename))
	if !audioExt[ext] {
		return nil, fmt.Errorf("%s is not an audio format the watch plays (mp3, m4a, aac, wav, flac)", filename)
	}

	folder = titleCase(folder)
	ok := false

	for _, f := range MusicFolders {
		if f == folder {
			ok = true
		}
	}

	if !ok {
		folder = "Music"
	}

	tags := ReadTags(data)
	base := strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	title := safeName(tags.Title, safeName(base, "track"))

	var target string

	if tags.Artist != "" {
		album := safeName(tags.Album, "Unknown Album")
		target = fmt.Sprintf("%s/%s/%s/%s%s", folder, safeName(tags.Artist, "Unknown Artist"), album, title, ext)
	} else {
		target = fmt.Sprintf("%s/%s%s", folder, title, ext)
	}

	if err := s.ensureDir(ctx, path.Dir(target)); err != nil {
		return nil, err
	}

	if err := s.dev.Write(ctx, target, data); err != nil {
		return nil, err
	}

	return &Track{Path: target, Folder: folder, Artist: tags.Artist, Album: tags.Album, Title: title, Size: int64(len(data))}, nil
}

func musicPath(p string) error {
	p = strings.ReplaceAll(p, "\\", "/")
	for _, f := range MusicFolders {
		if strings.HasPrefix(p, f+"/") {
			return nil
		}
	}

	return fmt.Errorf("%s is not inside Music, Podcasts or Audiobooks", p)
}

// RemoveTrack deletes an audio file or playlist.
func (s *Service) RemoveTrack(ctx context.Context, p string) error {
	if err := musicPath(p); err != nil {
		return err
	}

	return s.dev.Delete(ctx, p)
}

// WritePlaylist writes Music/<name>.m3u listing the given watch paths.
func (s *Service) WritePlaylist(ctx context.Context, name string, tracks []string) (*Playlist, error) {
	name = safeName(name, "")
	if name == "" {
		return nil, errors.New("playlist needs a name")
	}

	if len(tracks) == 0 {
		return nil, errors.New("playlist needs tracks")
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")

	for _, t := range tracks {
		if err := musicPath(t); err != nil {
			return nil, err
		}

		b.WriteString(strings.TrimPrefix(t, "Music/") + "\n")
	}

	p := "Music/" + name + ".m3u"

	if err := s.ensureDir(ctx, "Music"); err != nil {
		return nil, err
	}

	if err := s.dev.Write(ctx, p, []byte(b.String())); err != nil {
		return nil, err
	}

	return &Playlist{Name: name, Path: p, Tracks: tracks}, nil
}
