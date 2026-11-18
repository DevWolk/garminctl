package music

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

type optionalReadFailure struct {
	device.Device
	reads int
}

func (d *optionalReadFailure) Read(context.Context, string) ([]byte, error) {
	d.reads++
	return nil, errors.New("optional file unavailable")
}

func TestListKeepsTracksWhenOptionalReadsFail(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.MusicRoot(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	failed := &optionalReadFailure{Device: dev}

	lib, err := (ListAction{}).Run(t.Context(), watchsession.New(failed))
	if err != nil || len(lib.Tracks) != 3 || lib.Playlists == nil || len(lib.Playlists) != 0 || lib.Providers == nil || len(lib.Providers) != 0 || failed.reads != 2 {
		t.Fatalf("library=%+v error=%v optional reads=%d", lib, err, failed.reads)
	}
}

func TestTrackAndPlaylistBytesRemainUnchanged(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(testdump.MusicRoot(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	sess := watchsession.New(dev)
	payload := testdump.MusicPayload()

	track, err := (AddTrackAction{}).Run(t.Context(), sess, "unknown", "input.MP3", payload)
	if err != nil || track.Path != "Music/Artist Name/Album/Fixture Song.mp3" || track.Artist != "Artist/Name" {
		t.Fatalf("track=%+v error=%v", track, err)
	}

	written, err := dev.Read(t.Context(), track.Path)
	if err != nil || !bytes.Equal(written, payload) {
		t.Fatalf("audio bytes changed: %v", err)
	}

	tracks := []string{track.Path, "Podcasts/episode.aac"}

	pl, err := (WritePlaylistAction{}).Run(t.Context(), sess, "  Mix:/  ", tracks)
	if err != nil {
		t.Fatal(err)
	}

	before, err := dev.Read(t.Context(), pl.Path)
	if err != nil || string(before) != "#EXTM3U\nArtist Name/Album/Fixture Song.mp3\nPodcasts/episode.aac\n" {
		t.Fatalf("playlist=%q error=%v", before, err)
	}

	_, err = (WritePlaylistAction{}).Run(t.Context(), sess, "Mix", []string{track.Path, "GARMIN/other.fit"})
	if err == nil {
		t.Fatal("invalid second track was accepted")
	}

	after, err := dev.Read(t.Context(), pl.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed edit changed playlist: %v", err)
	}
}

func TestInvalidAudioDoesNotInvalidateOrWrite(t *testing.T) {
	t.Parallel()

	dev, err := device.NewDirFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	sess := watchsession.New(dev)
	_, err = (AddTrackAction{}).Run(t.Context(), sess, "Music", "input.txt", []byte("not audio"))

	gen, _ := sess.CacheGeneration()
	if err == nil || !strings.Contains(err.Error(), "not an audio format") || gen != 0 {
		t.Fatalf("error=%v generation=%d", err, gen)
	}
}
