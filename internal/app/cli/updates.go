package cli

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	updatescli "github.com/DevWolk/garminctl/internal/containers/updates/ui/cli"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) updatesCLI(sess *watchsession.Session) *updatescli.Command {
	return &updatescli.Command{
		Config:      app.WatchUpdateConfig(),
		Installed:   app.UpdatesRegistry(sess),
		Invalidated: c.runtime.AppSchemaCacheFor(sess).Reset,
		Out:         c.out,
		ErrOut:      c.errOut,
		JSON:        c.asJSON,
	}
}

func (c *cli) updates(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.updatesCLI(sess).Updates(ctx, sess, args)
}

func (c *cli) software(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.updatesCLI(sess).Software(ctx, sess, args)
}
