package cli

import (
	"context"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) backup(ctx context.Context, args []string) error {
	return c.watchCLI().Backup(ctx, &backupSource{cli: c}, args)
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
