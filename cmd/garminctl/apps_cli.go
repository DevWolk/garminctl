package main

import (
	"context"

	appscli "github.com/DevWolk/garminctl/internal/containers/apps/ui/cli"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) appsCLI() *appscli.Commands {
	return &appscli.Commands{Out: c.out, JSON: c.asJSON, CacheFor: c.runtime.AppSchemaCacheFor}
}

func (c *cli) apps(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().List(ctx, sess, args)
}

func (c *cli) appHide(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().Hide(ctx, sess, args)
}

func (c *cli) appShow(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().Show(ctx, sess, args)
}

func (c *cli) appRestore(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().Restore(ctx, sess, args)
}

func (c *cli) appPending(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().Pending(ctx, sess, args)
}

func (c *cli) appRm(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().Remove(ctx, sess, args)
}

func (c *cli) appSettings(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().Settings(ctx, sess, args)
}

func (c *cli) appSchema(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.appsCLI().Schema(ctx, sess, args)
}
