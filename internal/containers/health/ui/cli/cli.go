// Package cli adapts health Actions to existing CLI flags, output and errors.
package cli

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Command receives formatting state and caller-scoped account/profile access.
type Command struct {
	Out     *clix.Printer
	ErrOut  *clix.Printer
	JSON    bool
	Profile *fit.Profile
	Access  health.AccountAccess
}

const (
	defaultActivities = 30
	defaultHealthDays = 14
	defaultHistory    = 90
	maxBadgesShown    = 10
)

// Activities lists through the caller-provided shared runtime reader.
func (c *Command) Activities(ctx context.Context, read func(context.Context, bool) (*health.ActivityList, error), args []string) error {
	fs := flag.NewFlagSet("activities", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	n := fs.Int("n", defaultActivities, "how many to show")
	wait := fs.Bool("wait", false, "decode every file before answering")

	if err := clix.ParseFlags(fs, clix.FlagsFirst(fs, args)); err != nil {
		return err
	}

	list, err := read(ctx, *wait)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, list)
	}

	for i, a := range list.Activities {
		if i >= *n {
			break
		}

		c.Out.Printf("%-28s %-16s %-10s %7.2f km %8s %4d kcal  HR %3d/%3d\n", a.File, a.Start.Local().Format("2006-01-02 15:04"), a.Sport, a.Distance/1000, health.FormatDuration(a.Timer), a.Calories, a.AvgHR, a.MaxHR)
	}

	if list.Pending > 0 {
		c.Out.Printf("%d of %d files still being decoded in the background; run again (or use -wait)\n", list.Pending, list.Total)
	}

	return nil
}

// Activity decodes within an existing operation lease.
func (c *Command) Activity(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) != 1 {
		return clix.UsageErrorf("usage: activity FILE")
	}

	d, err := (health.ReadActivityAction{Profile: c.Profile}).Run(ctx, sess, args[0], 0) // default track resolution
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, d)
	}

	c.Out.Printf("%s %s %s: %.2f km in %s, %d kcal, HR %d avg / %d max, %d m up, %d points, %d laps\n",
		d.File, d.Start.Local().Format("2006-01-02 15:04"), d.Sport, d.Distance/1000, health.FormatDuration(d.Timer), d.Calories, d.AvgHR, d.MaxHR, d.Ascent, len(d.Points), len(d.Laps))

	for i, l := range d.Laps {
		c.Out.Printf("  lap %2d  %8s  %6.2f km  HR %3d\n", i+1, health.FormatDuration(l.Elapsed), l.Distance/1000, l.AvgHR)
	}

	return nil
}

// Health preserves subcommand dispatch and account error ordering.
func (c *Command) Health(ctx context.Context, args []string) error {
	cloud := c.Access

	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "extras":
		return c.healthExtras(ctx, cloud)
	case "weight":
		if len(args) < 2 {
			return clix.UsageErrorf("usage: health weight KG")
		}

		return c.healthWeight(ctx, cloud, args[1])
	case "history":
		n := defaultHistory

		if len(args) >= 2 {
			var err error

			n, err = strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				return clix.UsageErrorf("usage: health history [DAYS]  (DAYS is a positive number, got %q)", args[1])
			}
		}

		return c.healthHistory(ctx, cloud, n)
	case "day":
		if len(args) < 2 {
			return clix.UsageErrorf("usage: health day YYYY-MM-DD")
		}

		return c.healthDay(ctx, cloud, args[1])
	}

	return c.healthDaily(ctx, dailyReader{access: cloud}, args)
}

func (c *Command) healthExtras(ctx context.Context, cloud health.AccountAccess) error {
	x, err := (health.ExtrasAction{Access: cloud}).Run(ctx)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, x)
	}

	if x.VO2Max > 0 {
		c.Out.Printf("VO2 max %.1f (%s)\n", x.VO2Max, x.VO2MaxDate)
	}

	if x.FitnessAge > 0 {
		c.Out.Printf("fitness age %.0f (chronological %d)\n", x.FitnessAge, x.ChronologicalA)
	}

	if x.HRV != nil {
		c.Out.Printf("HRV last night %d ms, weekly %d ms, %s\n", x.HRV.LastNight, x.HRV.WeeklyAvg, x.HRV.Status)
	}

	for _, r := range x.Records {
		c.Out.Printf("record  %-24s %-12s %s\n", r.Label, r.Display, r.Date)
	}

	for i, b := range x.Badges {
		if i >= maxBadgesShown {
			c.Out.Printf("… %d more badges\n", len(x.Badges)-maxBadgesShown)
			break
		}

		c.Out.Printf("badge   %-32s %s (%d pt)\n", b.Name, b.Date, b.Points)
	}

	for _, w := range x.Weight {
		c.Out.Printf("weight  %s %.1f kg\n", w.Date, w.Kg)
	}

	for _, e := range x.Errors {
		c.Out.Println("error:", e)
	}

	return nil
}

func (c *Command) healthWeight(ctx context.Context, cloud health.AccountAccess, arg string) error {
	kg, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		return fmt.Errorf("weight: %w", err)
	}

	if err := (health.LogWeightAction{Access: cloud}).Run(ctx, kg, time.Time{}); err != nil {
		return err
	}

	c.Out.Printf("logged %.1f kg in Garmin Connect\n", kg)

	return nil
}

func (c *Command) healthHistory(ctx context.Context, cloud health.AccountAccess, days int) error {
	rows, err := (health.HistoryAction{Access: cloud}).Run(ctx, days)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, rows)
	}

	for _, r := range rows {
		c.Out.Printf("%s\n", historyLine(r))
	}

	return nil
}

// historyLine formats one history row; a field the source did not provide
// prints as "-" and a day without data prints its error.
func historyLine(r health.DailyHealth) string {
	if r.Error != "" {
		return r.Date + " " + r.Error
	}

	battery := fmt.Sprintf("%3d-%3d", r.BodyBatteryLow, r.BodyBatteryHigh)

	return fmt.Sprintf("%s steps %6s  rhr %3s  stress %3s  bb %s  sleep %s", r.Date, healthInt(r, "steps", r.Steps), healthInt(r, "restingHr", r.RestingHR), healthInt(r, "avgStress", r.AvgStress), healthCell(r, "bodyBatteryHigh", battery), healthCell(r, "sleepSec", health.FormatDuration(float64(r.SleepSec))))
}

// dailyLine formats one row of the daily table with the same rules.
func dailyLine(d health.DailyHealth) string {
	if d.Error != "" {
		return fmt.Sprintf("%-10s %s", d.Date, d.Error)
	}

	battery := fmt.Sprintf("%3d-%-3d", d.BodyBatteryLow, d.BodyBatteryHigh)

	return fmt.Sprintf("%-10s %6s %5s %4s %6s %7s %7s %5s", d.Date, healthInt(d, "steps", d.Steps), healthInt(d, "stepGoal", d.StepGoal), healthInt(d, "restingHr", d.RestingHR), healthInt(d, "avgStress", d.AvgStress), healthCell(d, "bodyBatteryHigh", battery), healthCell(d, "sleepSec", health.FormatDuration(float64(d.SleepSec))), healthInt(d, "calories", d.Calories))
}

// healthCell returns value, or "-" when the row lacks the named JSON field.
func healthCell(d health.DailyHealth, field, value string) string {
	if d.Lacks(field) {
		return "-"
	}

	return value
}

func healthInt(d health.DailyHealth, field string, value int) string {
	return healthCell(d, field, strconv.Itoa(value))
}

func (c *Command) healthDay(ctx context.Context, cloud health.AccountAccess, date string) error {
	d, err := (health.DayAction{Access: cloud}).Run(ctx, date)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, d)
	}

	c.Out.Printf("%s: %d heart-rate samples, %d stress samples, %d body-battery samples\n", d.Date, len(d.HeartRate), len(d.Stress), len(d.BodyBattery))

	if s := d.Sleep; s != nil {
		c.Out.Printf("sleep: deep %s, light %s, REM %s, awake %s, score %d %s\n",
			health.FormatDuration(float64(s.DeepSec)), health.FormatDuration(float64(s.LightSec)), health.FormatDuration(float64(s.RemSec)), health.FormatDuration(float64(s.AwakeSec)), s.Score, s.Quality)
	}

	for _, e := range d.Errors {
		c.Out.Println("warning:", e)
	}

	return nil
}

type dailyHealthReader interface {
	Daily(context.Context, int) ([]health.DailyHealth, error)
	History(context.Context, int) ([]health.DailyHealth, error)
}

func (c *Command) healthDaily(ctx context.Context, cloud dailyHealthReader, args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	days := fs.Int("days", defaultHealthDays, "days to show")

	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	var (
		rows []health.DailyHealth
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

	if c.JSON {
		return clix.PrintJSON(c.Out, rows)
	}

	c.Out.Printf("%-10s %6s %5s %4s %6s %8s %7s %5s\n", "date", "steps", "goal", "rHR", "stress", "battery", "sleep", "kcal")

	for _, d := range rows {
		c.Out.Printf("%s\n", dailyLine(d))
	}

	return nil
}

type dailyReader struct{ access health.AccountAccess }

func (r dailyReader) Daily(ctx context.Context, days int) ([]health.DailyHealth, error) {
	return (health.DailyAction{Access: r.access}).Run(ctx, days)
}

func (r dailyReader) History(ctx context.Context, days int) ([]health.DailyHealth, error) {
	return (health.HistoryAction{Access: r.access}).Run(ctx, days)
}
