package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
)

func (c *cli) workouts(ctx context.Context, svc *garmin.Service, args []string) error {
	cloud := garmin.NewCloud(svc, gc.New(gc.DefaultStorePath()))

	if len(args) == 0 {
		return c.workoutsList(ctx, cloud)
	}

	switch args[0] {
	case "create":
		return c.workoutsCreate(ctx, svc, cloud, args[1:])
	case "send":
		if len(args) < 2 {
			return usageErrorf("usage: workouts send ID")
		}

		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}

		return c.reportPulled(cloud.SendWorkout(ctx, id, ""))
	}

	return usageErrorf("unknown workouts command %q", args[0])
}

func (c *cli) workoutsList(ctx context.Context, cloud *garmin.Cloud) error {
	list, err := cloud.Workouts(ctx, 0) // default page
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(list)
	}

	for _, w := range list {
		c.out.Printf("%-12d %-32s %-10s %8s %s\n", w.ID, trunc(w.Name, 32), w.Sport, garmin.FormatDuration(float64(w.Duration)), w.Updated)
	}

	return nil
}

func (c *cli) workoutsCreate(ctx context.Context, svc *garmin.Service, cloud *garmin.Cloud, args []string) error {
	fs := c.flagSet("workouts create")
	send := fs.Bool("send", false, "send to the watch through Garmin Connect")
	local := fs.Bool("local", false, "save only to the watch, without Garmin Connect")

	if err := parseFlags(fs, flagsFirst(fs, args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return usageErrorf("usage: workouts create FILE.json [-send] [-local]")
	}

	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}

	var spec garmin.WorkoutSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		return fmt.Errorf("spec: %w", err)
	}

	if *local {
		p, err := svc.WriteWorkoutToWatch(ctx, &spec)
		if err != nil {
			return err
		}

		c.out.Println("written to", p, "- unplug the watch to load the workout")

		return nil
	}

	wk, err := cloud.CreateWorkout(ctx, &spec)
	if err != nil {
		return err
	}

	c.out.Printf("saved in Garmin Connect as workout %d\n", wk.ID)

	if !*send {
		return nil
	}

	return c.reportPulled(cloud.SendWorkout(ctx, wk.ID, wk.Name))
}

// reportPulled prints what Connect wrote to the watch after queuing a workout.
func (c *cli) reportPulled(pulled []garmin.PulledFile, err error) error {
	if err != nil {
		return err
	}

	for _, p := range pulled {
		c.out.Println("written to the watch:", p.Path)
	}

	if len(pulled) == 0 {
		c.out.Println("queued for the watch; sync later")
	}

	return nil
}
