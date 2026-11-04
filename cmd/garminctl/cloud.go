package main

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	connectcli "github.com/DevWolk/garminctl/internal/containers/connect/ui/cli"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) cloudCommand(client *gc.Client) *connectcli.Command {
	return &connectcli.Command{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON, Client: client}
}

func (c *cli) cloudAccount(ctx context.Context, args []string) error {
	return c.cloudCommand(gc.New(gc.DefaultStorePath())).Account(ctx, args)
}

// cloud keeps the compatibility dispatch and initial lease order.
// The adapter borrows the already leased Session; sync instead uses job groups.
func (c *cli) cloud(ctx context.Context, _ *garmin.Service, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: cloud status|sync|messages|settings")
	}

	client := gc.New(gc.DefaultStorePath())
	command := c.cloudCommand(client)

	var sess *watchsession.Session
	if c.runtime != nil {
		sess = c.runtime.Watch.Manager().Current()
	}

	if c.runtime != nil {
		command.Cloud = app.ConnectFor(c.runtime.Watch, client)(sess)
	} else {
		command.Cloud = connect.NewCloud(sess, client)
	}

	command.RunSync = func(ctx context.Context, up, down bool, progress connect.Progress) (*connect.SyncResult, error) {
		if c.runtime == nil {
			return (connect.SyncAction{Cloud: command.Cloud}).Run(ctx, up, down, progress)
		}

		managed := app.JobConnect(c.runtime.Watch, gc.New(gc.DefaultStorePath()), c.epoch)

		job, _, err := c.runtime.Sync.Start(ctx, connect.SyncOptions{Upload: up, Download: down, Epoch: c.epoch}, func(ctx context.Context, _ connect.Progress) (*connect.SyncResult, error) {
			return (connect.SyncAction{Cloud: managed}).Run(ctx, up, down, progress)
		})
		if err != nil {
			return nil, err
		}

		return job.Wait(ctx)
	}

	return command.Run(ctx, args)
}
