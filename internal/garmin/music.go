package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/music"
)

// MusicFolders retains the initial media-root slice for compatibility.
var MusicFolders = music.MusicFolders

// Track preserves the music-owned audio model.
type (
	Track = music.Track
	// Playlist preserves the music-owned playlist model.
	Playlist = music.Playlist
	// MusicLibrary preserves the music-owned library model.
	MusicLibrary = music.Library
	// AudioProvider preserves the music-owned streaming metadata model.
	AudioProvider = music.AudioProvider
	// Tags preserves the music-owned ID3 model.
	Tags = music.Tags
)

// ReadTags delegates the unchanged ID3 codec.
func ReadTags(data []byte) Tags { return music.ReadTags(data) }

// Music delegates media listing through the same Session.
func (s *Service) Music(ctx context.Context) (*MusicLibrary, error) {
	return (music.ListAction{}).Run(ctx, s.session)
}

// AddTrack delegates audio staging through the same Session.
func (s *Service) AddTrack(ctx context.Context, folder, filename string, data []byte) (*Track, error) {
	return (music.AddTrackAction{}).Run(ctx, s.session, folder, filename, data)
}

// RemoveTrack delegates the existing path policy and removal.
func (s *Service) RemoveTrack(ctx context.Context, p string) error {
	return (music.RemoveTrackAction{}).Run(ctx, s.session, p)
}

// WritePlaylist delegates M3U serialization and writing.
func (s *Service) WritePlaylist(ctx context.Context, name string, tracks []string) (*Playlist, error) {
	return (music.WritePlaylistAction{}).Run(ctx, s.session, name, tracks)
}
