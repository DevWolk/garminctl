package cli

import (
	"context"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

// Backup copies watch storage using the caller's job-scoped file source.
func (c *Commands) Backup(ctx context.Context, source watch.BackupSource, args []string) error {
	fs := c.flagSet("backup")

	skip := fs.String("skip", "Music,Podcasts,Audiobooks", "comma-separated top-level folders to leave out")
	if err := clix.ParseFlags(fs, clix.FlagsFirst(fs, args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return clix.UsageErrorf("usage: backup DIR")
	}

	observer := watch.BackupObserver{
		Copied: func(path string, bytes int) {
			if !c.JSON {
				c.Out.Printf("  %s (%d bytes)\n", path, bytes)
			}
		},
		Skipped: func(path string, err error) { c.ErrOut.Printf("skip %s: %v\n", path, err) },
	}

	result, err := watch.NewBackupAction(source, observer).Run(ctx, watch.BackupRequest{
		Destination: fs.Arg(0), Skip: strings.Split(*skip, ","),
	})
	if err != nil {
		return err
	}

	c.Out.Printf("backed up %d files, %.1f MB, to %s\n", result.Files, float64(result.Bytes)/1e6, fs.Arg(0))

	return nil
}
