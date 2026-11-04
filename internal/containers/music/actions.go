package music

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// ListAction lists local files, playlists and XML-reported provider caches.
type ListAction struct{}

// Run retains track ordering and optional playlist/provider error handling.
func (ListAction) Run(ctx context.Context, sess *watchsession.Session) (*Library, error) {
	lib := &Library{Tracks: []Track{}, Playlists: []Playlist{}, Providers: []AudioProvider{}}

	playlistFiles, err := collectTracks(ctx, sess, lib)
	if err != nil {
		return nil, err
	}

	sort.Slice(lib.Tracks, func(i, j int) bool { return lib.Tracks[i].Path < lib.Tracks[j].Path })

	for _, pf := range playlistFiles {
		pl, err := readPlaylist(ctx, sess, pf)
		if err != nil {
			continue
		}

		lib.Playlists = append(lib.Playlists, pl)
	}

	lib.Providers = append(lib.Providers, audioProviders(ctx, sess)...)

	return lib, nil
}

// AddTrackAction writes audio to its tag-derived media location.
type AddTrackAction struct{}

// Run validates the extension, resolves tags/folders and writes unchanged bytes.
func (AddTrackAction) Run(ctx context.Context, sess *watchsession.Session, folder, filename string, data []byte) (*Track, error) {
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

	if err := sess.EnsureDir(ctx, path.Dir(target)); err != nil {
		return nil, err
	}

	if err := sess.WriteFile(ctx, target, data); err != nil {
		return nil, err
	}

	return &Track{Path: target, Folder: folder, Artist: tags.Artist, Album: tags.Album, Title: title, Size: int64(len(data))}, nil
}

// RemoveTrackAction removes an audio file or playlist within a media root.
type RemoveTrackAction struct{}

// Run retains the existing media-path check before deletion.
func (RemoveTrackAction) Run(ctx context.Context, sess *watchsession.Session, p string) error {
	if err := musicPath(p); err != nil {
		return err
	}

	return sess.DeleteFile(ctx, p)
}

// WritePlaylistAction writes an M3U file from existing watch paths.
type WritePlaylistAction struct{}

// Run preserves sanitization, relative Music paths and caller track order.
func (WritePlaylistAction) Run(ctx context.Context, sess *watchsession.Session, name string, tracks []string) (*Playlist, error) {
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

	if err := sess.EnsureDir(ctx, "Music"); err != nil {
		return nil, err
	}

	if err := sess.WriteFile(ctx, p, []byte(b.String())); err != nil {
		return nil, err
	}

	return &Playlist{Name: name, Path: p, Tracks: tracks}, nil
}
