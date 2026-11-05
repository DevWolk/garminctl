package main

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/health"
	healthcli "github.com/DevWolk/garminctl/internal/containers/health/ui/cli"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) healthCLI(access health.AccountAccess) *healthcli.Command {
	return &healthcli.Command{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON, Profile: fit.MustProfile(), Access: access}
}

// activities reads through the runtime's shared activity coordinator.
func (c *cli) activities(ctx context.Context, args []string) error {
	return c.healthCLI(health.AccountAccess{}).Activities(ctx, c.runtime.Activities.Activities, args)
}

func (c *cli) activity(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.healthCLI(health.AccountAccess{}).Activity(ctx, sess, args)
}

func (c *cli) health(ctx context.Context, sess *watchsession.Session, args []string) error {
	account := gc.New(gc.DefaultStorePath())
	return c.healthCLI(app.HealthAccess(c.runtime.Watch, account)(sess)).Health(ctx, args)
}
