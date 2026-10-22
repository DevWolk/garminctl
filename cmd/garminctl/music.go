package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/garmin"
)

func (c *cli) music(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}

	switch args[0] {
	case "ls":
		return c.musicList(ctx, svc)
	case "add":
		return c.musicAdd(ctx, svc, args[1:])
	case "rm":
		for _, p := range args[1:] {
			if err := svc.RemoveTrack(ctx, p); err != nil {
				return err
			}

			c.out.Println("removed", p)
		}

		return nil
	case "playlist":
		if len(args) < 3 {
			return usageErrorf("usage: music playlist NAME TRACK [TRACK ...]")
		}

		pl, err := svc.WritePlaylist(ctx, args[1], args[2:])
		if err != nil {
			return err
		}

		c.out.Println("written", pl.Path)

		return nil
	}

	return usageErrorf("unknown music command %q", args[0])
}

func (c *cli) musicList(ctx context.Context, svc *garmin.Service) error {
	lib, err := svc.Music(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(lib)
	}

	for _, t := range lib.Tracks {
		c.out.Printf("%-60s %6.1f MB\n", t.Path, float64(t.Size)/1e6)
	}

	c.out.Printf("%d tracks, %.1f MB\n", len(lib.Tracks), float64(lib.Bytes)/1e6)

	for _, p := range lib.Playlists {
		c.out.Printf("playlist %s: %d tracks\n", p.Name, len(p.Tracks))
	}

	for _, p := range lib.Providers {
		c.out.Printf("%s v%d keeps %.1f MB of downloads in its protected cache (hidden from USB)\n", p.Name, p.Version, float64(p.CacheBytes)/1e6)
	}

	return nil
}

func (c *cli) musicAdd(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := c.flagSet("music add")
	folder := fs.String("folder", "Music", "Music, Podcasts or Audiobooks")

	if err := parseFlags(fs, flagsFirst(fs, args)); err != nil {
		return err
	}

	for _, f := range fs.Args() {
		data, err := os.ReadFile(f) //nolint:gosec // the files are named by the user on the command line
		if err != nil {
			return err
		}

		t, err := svc.AddTrack(ctx, *folder, filepath.Base(f), data)
		if err != nil {
			return err
		}

		c.out.Println("added", t.Path)
	}

	return nil
}
