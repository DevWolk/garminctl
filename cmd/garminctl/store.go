package main

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	storecli "github.com/DevWolk/garminctl/internal/containers/store/ui/cli"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) store(ctx context.Context, sess *watchsession.Session, args []string) error {
	return (&storecli.Command{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON, Installed: app.StoreRegistry(sess)}).Run(ctx, sess, args)
}
