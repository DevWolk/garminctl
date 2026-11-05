package cli

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	watchcli "github.com/DevWolk/garminctl/internal/containers/watch/ui/cli"
	"github.com/DevWolk/garminctl/internal/devicehost"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) watchCLI() *watchcli.Commands {
	return &watchcli.Commands{Profile: fit.MustProfile(), AppCount: app.WatchAppCount, Out: c.out, ErrOut: c.errOut, JSON: c.asJSON}
}

func (c *cli) devices(ctx context.Context, args []string) error {
	command := &watchcli.Commands{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON, Discover: devicehost.Devices}
	return command.Devices(ctx, args)
}

func (c *cli) info(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().Info(ctx, sess, args)
}

func (c *cli) ls(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().List(ctx, sess, args)
}

func (c *cli) cat(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().Cat(ctx, sess, args)
}

func (c *cli) put(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().Put(ctx, sess, args)
}

func (c *cli) rm(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().Remove(ctx, sess, args)
}

func (c *cli) mkdir(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().Mkdir(ctx, sess, args)
}

func (c *cli) hidden(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().Hidden(ctx, sess, args)
}

func (c *cli) mtp(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().MTP(ctx, sess, args)
}

func (c *cli) decode(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.watchCLI().Decode(ctx, sess, args)
}
