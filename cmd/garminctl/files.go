package main

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) backup(ctx context.Context, legacy *garmin.Service, args []string) error {
	var source watch.BackupSource = &backupSource{cli: c}
	if c.runtime == nil {
		source = legacy
	}

	return c.watchCLI().Backup(ctx, source, args)
}

// backupSource releases each operation lease before host I/O/progress. The
// existing CLI job wrapper retains admission and epoch binding for the job.
type backupSource struct{ cli *cli }

func (b *backupSource) List(ctx context.Context, path string) ([]device.Entry, error) {
	var entries []device.Entry

	err := b.cli.withSessionAt(ctx, b.cli.epoch, func(ctx context.Context, sess *watchsession.Session) error {
		var err error

		entries, err = sess.List(ctx, path)

		return err
	})

	return entries, err
}

func (b *backupSource) ReadFile(ctx context.Context, path string) ([]byte, error) {
	var data []byte

	err := b.cli.withSessionAt(ctx, b.cli.epoch, func(ctx context.Context, sess *watchsession.Session) error {
		var err error

		data, err = sess.ReadFile(ctx, path)

		return err
	})

	return data, err
}
