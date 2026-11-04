// Package cli adapts local music Actions to bounded commands and per-file jobs.
package cli

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/music"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WithSession scopes one bounded operation within the caller's epoch-bound job.
type (
	WithSession func(context.Context, func(context.Context, *watchsession.Session) error) error
	// Command receives output writers and leases from the entry point, not an owner.
	Command struct {
		Out    *clix.Printer
		ErrOut *clix.Printer
		JSON   bool
	}
)

// Run handles bounded listing/playlist operations with an already held Session.
func (c *Command) Run(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}

	switch args[0] {
	case "ls":
		return c.list(ctx, sess)
	case "playlist":
		if len(args) < 3 {
			return clix.UsageErrorf("usage: music playlist NAME TRACK [TRACK ...]")
		}

		pl, err := (music.WritePlaylistAction{}).Run(ctx, sess, args[1], args[2:])
		if err != nil {
			return err
		}

		c.Out.Println("written", pl.Path)

		return nil
	}

	return clix.UsageErrorf("unknown music command %q", args[0])
}

func (c *Command) list(ctx context.Context, sess *watchsession.Session) error {
	lib, err := (music.ListAction{}).Run(ctx, sess)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, lib)
	}

	for _, t := range lib.Tracks {
		c.Out.Printf("%-60s %6.1f MB\n", t.Path, float64(t.Size)/1e6)
	}

	c.Out.Printf("%d tracks, %.1f MB\n", len(lib.Tracks), float64(lib.Bytes)/1e6)

	for _, p := range lib.Playlists {
		c.Out.Printf("playlist %s: %d tracks\n", p.Name, len(p.Tracks))
	}

	for _, p := range lib.Providers {
		c.Out.Printf("%s v%d: %.1f MB download cache\n", p.Name, p.Version, float64(p.CacheBytes)/1e6)
	}

	return nil
}

// Add reads each host file before entering its operation lease. Progress retains
// its original position inside the write group; no Session survives that group.
func (c *Command) Add(ctx context.Context, args []string, withSession WithSession) error {
	fs := flag.NewFlagSet("music add", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	folder := fs.String("folder", "Music", "Music, Podcasts or Audiobooks")

	if err := clix.ParseFlags(fs, clix.FlagsFirst(fs, args)); err != nil {
		return err
	}

	for _, f := range fs.Args() {
		data, err := os.ReadFile(f) //nolint:gosec // the files are named by the user on the command line
		if err != nil {
			return err
		}

		if err := withSession(ctx, func(ctx context.Context, sess *watchsession.Session) error {
			track, err := (music.AddTrackAction{}).Run(ctx, sess, *folder, filepath.Base(f), data)
			if err == nil {
				c.Out.Println("added", track.Path)
			}

			return err
		}); err != nil {
			return err
		}
	}

	return nil
}

// Remove releases each operation before printing progress, preserving batching.
func (c *Command) Remove(ctx context.Context, paths []string, withSession WithSession) error {
	for _, p := range paths {
		if err := withSession(ctx, func(ctx context.Context, sess *watchsession.Session) error {
			return (music.RemoveTrackAction{}).Run(ctx, sess, p)
		}); err != nil {
			return err
		}

		c.Out.Println("removed", p)
	}

	return nil
}
