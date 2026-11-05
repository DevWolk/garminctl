package cli

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	connectcli "github.com/DevWolk/garminctl/internal/containers/connect/ui/cli"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) cloudCommand(client *gc.Client) *connectcli.Command {
	return &connectcli.Command{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON, Client: client}
}

func (c *cli) cloudAccount(ctx context.Context, args []string) error {
	return c.cloudCommand(gc.New(gc.DefaultStorePath())).Account(ctx, args)
}

// cloud runs the Connect commands that borrow the dispatcher's leased Session.
func (c *cli) cloud(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: cloud status|sync|messages|settings")
	}

	client := gc.New(gc.DefaultStorePath())
	command := c.cloudCommand(client)
	command.Cloud = app.ConnectFor(c.runtime, client)(sess)

	return command.Run(ctx, args)
}

// cloudSync runs sync as a runtime job bound to the epoch of the initial lease;
// it holds no lease itself, and the job takes its own operation groups.
func (c *cli) cloudSync(ctx context.Context, args []string) error {
	client := gc.New(gc.DefaultStorePath())
	command := c.cloudCommand(client)
	managed := app.JobConnect(c.runtime, client, c.epoch)

	command.RunSync = func(ctx context.Context, up, down bool, progress connect.Progress) (*connect.SyncResult, error) {
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
