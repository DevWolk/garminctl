package cli

import (
	"context"

	musiccli "github.com/DevWolk/garminctl/internal/containers/music/ui/cli"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const musicCommand = "music"

func (c *cli) musicCLI() *musiccli.Command {
	return &musiccli.Command{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON}
}

func (c *cli) music(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.musicCLI().Run(ctx, sess, args)
}

func (c *cli) musicJob(ctx context.Context, args []string) error {
	group := func(ctx context.Context, body func(context.Context, *watchsession.Session) error) error {
		return c.withSessionAt(ctx, c.epoch, body)
	}
	if args[0] == "add" {
		return c.musicCLI().Add(ctx, args[1:], group)
	}

	return c.musicCLI().Remove(ctx, args[1:], group)
}
