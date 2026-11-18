// Package cli adapts watch operations to command-line syntax and output.
package cli

import (
	"context"
	"flag"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/fit"
)

// Commands uses supplied writers and already leased Sessions. Discovery is a
// separate no-watch operation; the adapter owns no device or process lifecycle.
type Commands struct {
	Profile  *fit.Profile
	AppCount watch.AppCount
	Out      *clix.Printer
	ErrOut   *clix.Printer
	JSON     bool
	Discover func(context.Context) ([]device.Info, error)
}

func (c *Commands) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)

	return fs
}
