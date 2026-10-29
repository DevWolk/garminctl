package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
)

func (c *cli) backup(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := c.flagSet("backup")
	skip := fs.String("skip", "Music,Podcasts,Audiobooks", "comma-separated top-level folders to leave out")

	if err := parseFlags(fs, flagsFirst(fs, args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return usageErrorf("usage: backup DIR")
	}

	dest := fs.Arg(0)
	b := &backupRun{cli: c, svc: svc, skip: map[string]bool{}}

	for s := range strings.SplitSeq(*skip, ",") {
		if s = strings.TrimSpace(s); s != "" {
			b.skip[strings.ToLower(s)] = true
		}
	}

	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}

	// Every file is written through os.Root: a name the watch reports with
	// ".." or a separator cannot land outside DIR.
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()

	b.root = root

	if err := b.walk(ctx, ""); err != nil {
		return err
	}

	c.out.Printf("backed up %d files, %.1f MB, to %s\n", b.files, float64(b.bytes)/1e6, dest)

	return nil
}

// backupRun copies the watch's storage to a folder.
type backupRun struct {
	*cli
	svc   *garmin.Service
	root  *os.Root
	skip  map[string]bool
	files int
	bytes int
}

func (b *backupRun) walk(ctx context.Context, dir string) error {
	var entries []device.Entry

	err := b.withDeviceGroup(ctx, b.svc, func(ctx context.Context, svc *garmin.Service) error {
		var err error

		entries, err = svc.List(ctx, dir)

		return err
	})
	if err != nil {
		return err
	}

	for _, e := range entries {
		p := e.Name
		if dir != "" {
			p = dir + "/" + e.Name
		}

		if dir == "" && b.skip[strings.ToLower(e.Name)] {
			continue
		}

		if e.Dir {
			if err := b.walk(ctx, p); err != nil {
				return err
			}

			continue
		}

		if err := b.copyFile(ctx, p); err != nil {
			return err
		}
	}

	return nil
}

func (b *backupRun) copyFile(ctx context.Context, p string) error {
	var data []byte

	err := b.withDeviceGroup(ctx, b.svc, func(ctx context.Context, svc *garmin.Service) error {
		var err error

		data, err = svc.ReadFile(ctx, p)

		return err
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, device.ErrDisconnected) {
			return err
		}

		b.errOut.Printf("skip %s: %v\n", p, err)

		return nil
	}

	rel := filepath.FromSlash(p)
	if dir := filepath.Dir(rel); dir != "." {
		if err := b.root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}

	if err := b.root.WriteFile(rel, data, 0o600); err != nil {
		return err
	}

	b.files++
	b.bytes += len(data)

	if !b.asJSON {
		b.out.Printf("  %s (%d bytes)\n", p, len(data))
	}

	return nil
}
