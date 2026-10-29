package main

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// sessionCommand is the dispatch contract for extracted feature adapters.
type sessionCommand func(context.Context, *watchsession.Session, []string) error

func (c *cli) sessionCommands() map[string]sessionCommand {
	return map[string]sessionCommand{
		"settings": c.settings,
		"info":     c.info,
		"ls":       c.ls,
		"cat":      c.cat,
		"put":      c.put,
		"rm":       c.rm,
		"mkdir":    c.mkdir,
		"hidden":   c.hidden,
		"mtp":      c.mtp,
		"decode":   c.decode,
	}
}

// runSession uses the runtime's existing Manager without a legacy Service facade.
// runDevice owns runtime shutdown for both extracted and legacy commands.
func (c *cli) runSession(ctx context.Context, command sessionCommand, args []string) error {
	manager := c.runtime.Watch.Manager()

	lease, err := manager.Acquire(ctx)
	if err != nil {
		return err
	}

	err = command(lease.Context(), lease.Session(), args)
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, manager.DropAt(lease.Epoch()))
	}

	return errors.Join(err, lease.Release())
}
