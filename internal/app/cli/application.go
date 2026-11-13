package cli

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/application"
	applicationcli "github.com/DevWolk/garminctl/internal/containers/application/ui/cli"
)

const applicationCommandName = "application"

func (c *cli) application(ctx context.Context, args []string) error {
	command := applicationcli.Command{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON, Snapshots: app.NewApplicationRecoverySnapshots, Recovery: func() (*application.RecoveryAction, error) {
		return app.NewApplicationRecovery(c.buildVersion, c.updatePublicKey)
	}}

	return command.Run(ctx, args)
}
