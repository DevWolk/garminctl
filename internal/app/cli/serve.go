package cli

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

func (c *cli) serve(ctx context.Context, args []string) error {
	fs := c.flagSet("serve")
	addr := fs.String("addr", "127.0.0.1:8787", "listen address")
	autoSync := fs.Bool("autosync", true, "sync, check updates and notify when a watch connects")

	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	return app.Serve(ctx, *addr, c.opener(), *autoSync && c.root == "", c.buildVersion)
}

func (c *cli) mcp(ctx context.Context, _ []string) error {
	return app.ServeMCP(ctx, c.opener(), c.buildVersion)
}

func (c *cli) opener() func(ctx context.Context) (device.Device, error) {
	return func(ctx context.Context) (device.Device, error) {
		return openDevice(ctx, c.root, c.devID)
	}
}
