// Package cli adapts workout Actions to the existing command contract.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/DevWolk/garminctl/internal/containers/workouts"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Command borrows the caller's watch lease, account protocol and writers.
type Command struct {
	Out      *clix.Printer
	ErrOut   *clix.Printer
	JSON     bool
	Client   workouts.Account
	Delivery workouts.Delivery
}

// Run preserves workouts command dispatch, flags and output.
func (c *Command) Run(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) == 0 {
		return c.workoutsList(ctx)
	}

	switch args[0] {
	case "create":
		return c.workoutsCreate(ctx, sess, args[1:])
	case "send":
		if len(args) < 2 {
			return clix.UsageErrorf("usage: workouts send ID")
		}

		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}

		return c.reportPulled(c.send(ctx, id, ""))
	}

	return clix.UsageErrorf("unknown workouts command %q", args[0])
}

func (c *Command) workoutsList(ctx context.Context) error {
	list, err := (workouts.ListAction{Client: c.Client}).Run(ctx, 0) // default page
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, list)
	}

	for _, w := range list {
		c.Out.Printf("%-12d %-32s %-10s %8s %s\n", w.ID, trunc(w.Name, 32), w.Sport, formatDuration(float64(w.Duration)), w.Updated)
	}

	return nil
}

func (c *Command) workoutsCreate(ctx context.Context, sess *watchsession.Session, args []string) error {
	fs := flag.NewFlagSet("workouts create", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	send := fs.Bool("send", false, "send to the watch through Garmin Connect")
	local := fs.Bool("local", false, "save only to the watch, without Garmin Connect")

	if err := clix.ParseFlags(fs, clix.FlagsFirst(fs, args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return clix.UsageErrorf("usage: workouts create FILE.json [-send] [-local]")
	}

	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}

	var spec workouts.WorkoutSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		return fmt.Errorf("spec: %w", err)
	}

	if *local {
		p, err := (workouts.WriteLocalAction{}).Run(ctx, sess, &spec)
		if err != nil {
			return err
		}

		c.Out.Println("written to", p, "- unplug the watch to load the workout")

		return nil
	}

	wk, err := (workouts.CreateAction{Client: c.Client}).Run(ctx, &spec)
	if err != nil {
		return err
	}

	c.Out.Printf("saved in Garmin Connect as workout %d\n", wk.ID)

	if !*send {
		return nil
	}

	return c.reportPulled(c.send(ctx, wk.ID, wk.Name))
}

// reportPulled prints what Connect wrote to the watch after queuing a workout.
func (c *Command) reportPulled(pulled []workouts.PulledFile, err error) error {
	if err != nil {
		return err
	}

	for _, p := range pulled {
		if p.Error != "" {
			c.Out.Println("not written to the watch:", p.Error)
		} else {
			c.Out.Println("written to the watch:", p.Path)
		}
	}

	if written, _ := workouts.Written(pulled); written == 0 {
		c.Out.Println("queued for the watch; sync later")
	}

	return nil
}

func (c *Command) send(ctx context.Context, id int64, name string) ([]workouts.PulledFile, error) {
	return (workouts.SendAction{Client: c.Client, Queue: c.Delivery}).Run(ctx, id, name)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

func formatDuration(sec float64) string {
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}

	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
