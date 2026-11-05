package cli

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app"
	workoutscli "github.com/DevWolk/garminctl/internal/containers/workouts/ui/cli"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func (c *cli) workouts(ctx context.Context, sess *watchsession.Session, args []string) error {
	account := gc.New(gc.DefaultStorePath())
	command := &workoutscli.Command{Out: c.out, ErrOut: c.errOut, JSON: c.asJSON, Client: account, Delivery: app.WorkoutDelivery(c.runtime, account)(sess)}

	return command.Run(ctx, sess, args)
}
