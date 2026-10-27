package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
)

const (
	defaultActivities = 30
	defaultHealthDays = 14
	defaultHistory    = 90
	maxBadgesShown    = 10
)

func (c *cli) activities(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := c.flagSet("activities")
	n := fs.Int("n", defaultActivities, "how many to show")
	wait := fs.Bool("wait", false, "decode every file before answering")

	if err := parseFlags(fs, flagsFirst(fs, args)); err != nil {
		return err
	}

	var (
		list *garmin.ActivityList
		err  error
	)
	if c.runtime != nil {
		list, err = c.runtime.Activities.Activities(ctx, *wait)
	} else {
		list, err = svc.Activities(ctx, *wait)
	}

	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(list)
	}

	for i, a := range list.Activities {
		if i >= *n {
			break
		}

		c.out.Printf("%-28s %-16s %-10s %7.2f km %8s %4d kcal  HR %3d/%3d\n", a.File, a.Start.Local().Format("2006-01-02 15:04"), a.Sport, a.Distance/1000, garmin.FormatDuration(a.Timer), a.Calories, a.AvgHR, a.MaxHR)
	}

	if list.Pending > 0 {
		c.out.Printf("%d of %d files still being decoded in the background; run again (or use -wait)\n", list.Pending, list.Total)
	}

	return nil
}

func (c *cli) activity(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return usageErrorf("usage: activity FILE")
	}

	d, err := svc.Activity(ctx, args[0], 0) // default track resolution
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(d)
	}

	c.out.Printf("%s %s %s: %.2f km in %s, %d kcal, HR %d avg / %d max, %d m up, %d points, %d laps\n",
		d.File, d.Start.Local().Format("2006-01-02 15:04"), d.Sport, d.Distance/1000, garmin.FormatDuration(d.Timer), d.Calories, d.AvgHR, d.MaxHR, d.Ascent, len(d.Points), len(d.Laps))

	for i, l := range d.Laps {
		c.out.Printf("  lap %2d  %8s  %6.2f km  HR %3d\n", i+1, garmin.FormatDuration(l.Elapsed), l.Distance/1000, l.AvgHR)
	}

	return nil
}

func (c *cli) health(ctx context.Context, svc *garmin.Service, args []string) error {
	cloud := garmin.NewCloud(svc, gc.New(gc.DefaultStorePath()))

	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "extras":
		return c.healthExtras(ctx, cloud)
	case "weight":
		if len(args) < 2 {
			return usageErrorf("usage: health weight KG")
		}

		return c.healthWeight(ctx, cloud, args[1])
	case "history":
		n := defaultHistory

		if len(args) >= 2 {
			var err error

			n, err = strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				return usageErrorf("usage: health history [DAYS]  (DAYS is a positive number, got %q)", args[1])
			}
		}

		return c.healthHistory(ctx, cloud, n)
	case "day":
		if len(args) < 2 {
			return usageErrorf("usage: health day YYYY-MM-DD")
		}

		return c.healthDay(ctx, cloud, args[1])
	}

	return c.healthDaily(ctx, cloud, args)
}

func (c *cli) healthExtras(ctx context.Context, cloud *garmin.Cloud) error {
	x, err := cloud.Extras(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(x)
	}

	if x.VO2Max > 0 {
		c.out.Printf("VO2 max %.1f (%s)\n", x.VO2Max, x.VO2MaxDate)
	}

	if x.FitnessAge > 0 {
		c.out.Printf("fitness age %.0f (chronological %d)\n", x.FitnessAge, x.ChronologicalA)
	}

	if x.HRV != nil {
		c.out.Printf("HRV last night %d ms, weekly %d ms, %s\n", x.HRV.LastNight, x.HRV.WeeklyAvg, x.HRV.Status)
	}

	for _, r := range x.Records {
		c.out.Printf("record  %-24s %-12s %s\n", r.Label, r.Display, r.Date)
	}

	for i, b := range x.Badges {
		if i >= maxBadgesShown {
			c.out.Printf("… %d more badges\n", len(x.Badges)-maxBadgesShown)
			break
		}

		c.out.Printf("badge   %-32s %s (%d pt)\n", b.Name, b.Date, b.Points)
	}

	for _, w := range x.Weight {
		c.out.Printf("weight  %s %.1f kg\n", w.Date, w.Kg)
	}

	for _, e := range x.Errors {
		c.out.Println("error:", e)
	}

	return nil
}

func (c *cli) healthWeight(ctx context.Context, cloud *garmin.Cloud, arg string) error {
	kg, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		return fmt.Errorf("weight: %w", err)
	}

	if err := cloud.LogWeight(ctx, kg, time.Time{}); err != nil {
		return err
	}

	c.out.Printf("logged %.1f kg in Garmin Connect\n", kg)

	return nil
}

func (c *cli) healthHistory(ctx context.Context, cloud *garmin.Cloud, days int) error {
	rows, err := cloud.History(ctx, days)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(rows)
	}

	for _, r := range rows {
		c.out.Printf("%s steps %6d  rhr %3d  stress %3d  bb %3d-%3d  sleep %s\n", r.Date, r.Steps, r.RestingHR, r.AvgStress, r.BodyBatteryLow, r.BodyBatteryHigh, garmin.FormatDuration(float64(r.SleepSec)))
	}

	return nil
}

func (c *cli) healthDay(ctx context.Context, cloud *garmin.Cloud, date string) error {
	d, err := cloud.Day(ctx, date)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(d)
	}

	c.out.Printf("%s: %d heart-rate samples, %d stress samples, %d body-battery samples\n", d.Date, len(d.HeartRate), len(d.Stress), len(d.BodyBattery))

	if s := d.Sleep; s != nil {
		c.out.Printf("sleep: deep %s, light %s, REM %s, awake %s, score %d %s\n",
			garmin.FormatDuration(float64(s.DeepSec)), garmin.FormatDuration(float64(s.LightSec)), garmin.FormatDuration(float64(s.RemSec)), garmin.FormatDuration(float64(s.AwakeSec)), s.Score, s.Quality)
	}

	for _, e := range d.Errors {
		c.out.Println("warning:", e)
	}

	return nil
}

type dailyHealthReader interface {
	Daily(context.Context, int) ([]garmin.DailyHealth, error)
	History(context.Context, int) ([]garmin.DailyHealth, error)
}

func (c *cli) healthDaily(ctx context.Context, cloud dailyHealthReader, args []string) error {
	fs := c.flagSet("health")
	days := fs.Int("days", defaultHealthDays, "days to show")

	if err := parseFlags(fs, args); err != nil {
		return err
	}

	var (
		rows []garmin.DailyHealth
		err  error
	)
	if *days > defaultHealthDays {
		rows, err = cloud.History(ctx, *days)
	} else {
		rows, err = cloud.Daily(ctx, *days)
	}

	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(rows)
	}

	c.out.Printf("%-10s %6s %5s %4s %6s %8s %7s %5s\n", "date", "steps", "goal", "rHR", "stress", "battery", "sleep", "kcal")

	for _, d := range rows {
		if d.Error != "" {
			c.out.Printf("%-10s %s\n", d.Date, d.Error)
			continue
		}

		c.out.Printf("%-10s %6d %5d %4d %6d %3d-%-3d %7s %5d\n", d.Date, d.Steps, d.StepGoal, d.RestingHR, d.AvgStress, d.BodyBatteryLow, d.BodyBatteryHigh, garmin.FormatDuration(float64(d.SleepSec)), d.Calories)
	}

	return nil
}
