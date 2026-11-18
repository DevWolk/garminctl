package music

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/deviceinfo"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

var audioExt = map[string]bool{".mp3": true, ".m4a": true, ".aac": true, ".adts": true, ".wav": true, ".flac": true}

func walk(ctx context.Context, sess *watchsession.Session, dir string, fn func(p string, e device.Entry)) error {
	entries, err := sess.List(ctx, dir)
	if err != nil {
		if errors.Is(err, device.ErrNotFound) {
			return nil
		}

		return err
	}

	for _, e := range entries {
		p := dir + "/" + e.Name
		if e.Dir {
			if err := walk(ctx, sess, p, fn); err != nil {
				return err
			}

			continue
		}

		fn(p, e)
	}

	return nil
}

// collectTracks walks the media folders into lib and returns the playlist
// files it passed.
func collectTracks(ctx context.Context, sess *watchsession.Session, lib *Library) ([]string, error) {
	var playlistFiles []string

	for _, root := range MusicFolders {
		err := walk(ctx, sess, root, func(p string, e device.Entry) {
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
func readPlaylist(ctx context.Context, sess *watchsession.Session, pf string) (Playlist, error) {
	data, err := sess.ReadFile(ctx, pf)
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
func audioProviders(ctx context.Context, sess *watchsession.Session) []AudioProvider {
	d, err := sess.DeviceXML(ctx)
	if err != nil {
		return nil
	}

	var out []AudioProvider

	for _, a := range d.Apps {
		if a.Type == deviceinfo.AudioProviderType {
			out = append(out, AudioProvider{Name: a.Name, Version: a.Version, CacheBytes: a.MediaCacheBytes})
		}
	}

	return out
}
