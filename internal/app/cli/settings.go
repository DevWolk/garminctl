package cli

import (
	"context"

	settingscli "github.com/DevWolk/garminctl/internal/containers/settings/ui/cli"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) settings(ctx context.Context, sess *watchsession.Session, args []string) error {
	command := &settingscli.Command{Profile: fit.MustProfile(), Out: c.out, ErrOut: c.errOut, JSON: c.asJSON}
	return command.Run(ctx, sess, args)
}
