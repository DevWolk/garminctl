package main

import (
	"context"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/mcpserver"
	"github.com/DevWolk/garminctl/internal/web"
)

func (c *cli) serve(ctx context.Context, args []string) error {
	fs := c.flagSet("serve")
	addr := fs.String("addr", "127.0.0.1:8787", "listen address")
	autoSync := fs.Bool("autosync", true, "watch USB; when the watch appears, sync with Garmin Connect, check updates and notify")

	if err := parseFlags(fs, args); err != nil {
		return err
	}

	return web.Serve(ctx, *addr, c.opener(), *autoSync && c.root == "")
}

func (c *cli) mcp(ctx context.Context, _ []string) error {
	return mcpserver.Serve(ctx, c.opener(), version)
}

func (c *cli) opener() func(ctx context.Context) (device.Device, error) {
	return func(ctx context.Context) (device.Device, error) {
		return openDevice(ctx, c.root, c.devID)
	}
}
