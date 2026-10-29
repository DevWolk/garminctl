package testdump

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
)

// Watch returns a dump with stable transport metadata for contract snapshots.
// File contents use real fixtures; Info, listing order/times and Storage are
// synthetic inputs rather than values normalized out of an adapter response.
func Watch(tb testing.TB) (device.Device, string) {
	tb.Helper()

	root := Root(tb)
	for _, dir := range []string{"GARMIN/NewFiles", "Music", "Podcasts", "Audiobooks"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o750); err != nil {
			tb.Fatal(err)
		}
	}

	for path, data := range map[string][]byte{
		"GARMIN/notes.txt":              []byte("synthetic text\n"),
		"GARMIN/sample.bin":             {0, 1, 2, 3},
		"GARMIN/NewFiles/pending-a.FIT": []byte("pending-a"),
		"GARMIN/NewFiles/pending-b.FIT": []byte("pending-b"),
		"Music/song.mp3":                []byte("synthetic audio"),
		"Podcasts/episode.mp3":          []byte("synthetic podcast"),
		"Audiobooks/chapter.mp3":        []byte("synthetic audiobook"),
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), data, 0o600); err != nil {
			tb.Fatal(err)
		}
	}

	dir, err := device.NewDirFS(root)
	if err != nil {
		tb.Fatal(err)
	}

	return &contractWatch{Device: dir}, root
}

type contractWatch struct{ device.Device }

func (*contractWatch) Info() device.Info {
	return device.Info{ID: "fixture:watch", Name: "Fixture watch", Manufacturer: "Garmin", Description: "synthetic transport"}
}

func (d *contractWatch) List(ctx context.Context, path string) ([]device.Entry, error) {
	entries, err := d.Device.List(ctx, path)
	if err != nil {
		return nil, err
	}

	for i := range entries {
		entries[i].Modified = "2000-01-02T03:04:05"
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	return entries, nil
}

func (*contractWatch) Storage(context.Context) (device.Storage, error) {
	return device.Storage{CapacityBytes: 1_000_000_000, FreeBytes: 750_000_000, Description: "synthetic storage"}, nil
}
