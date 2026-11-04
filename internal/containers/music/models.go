// Package music owns local audio, playlists, tags and provider cache metadata.
package music

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

// Library is everything audio on the watch.
type Library struct {
	Tracks    []Track         `json:"tracks"`
	Playlists []Playlist      `json:"playlists"`
	Bytes     int64           `json:"bytes"`
	Providers []AudioProvider `json:"providers"` // streaming apps whose downloads are hidden from USB
}

// AudioProvider describes a streaming app from the device XML, including
// its reported cache size. Music does not enumerate provider downloads.
type AudioProvider struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	CacheBytes int64  `json:"cacheBytes"`
}
