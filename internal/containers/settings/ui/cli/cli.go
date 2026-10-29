// Package cli adapts watch settings Actions to command-line flags and output.
package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/DevWolk/garminctl/internal/containers/settings"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Command uses the entry point's output writers and an already leased Session.
// Run never opens, closes or retains the Session.
type Command struct {
	Profile *fit.Profile
	Out     *clix.Printer
	ErrOut  *clix.Printer
	JSON    bool
}

// Run reads settings or queues one settings-change file for the set subcommand.
func (c *Command) Run(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) > 0 && args[0] == "set" {
		return c.set(ctx, sess, args[1:])
	}

	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	all := fs.Bool("all", false, "include undocumented fields")

	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	view, err := settings.NewReadAction(c.Profile).Run(ctx, sess)
	if err != nil {
		return err
	}

	rows := view.Rows()
	if c.JSON {
		return clix.PrintJSON(c.Out, rows)
	}

	tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
	last := ""

	for _, r := range rows {
		if !*all && !r.Known && r.Note == "" {
			continue
		}

		head := fmt.Sprintf("%s[%d]", r.Mesg, r.Index)
		if head != last {
			fmt.Fprintf(tw, "%s\t\t\t\n", head)
			last = head
		}

		val := fmt.Sprint(r.Value)
		if r.Value == nil {
			val = "-"
		}

		note := r.Note
		if r.Units != "" {
			note = strings.TrimSpace(r.Units + " " + note)
		}

		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", r.Field, val, r.Type, note)
	}

	return tw.Flush()
}

func (c *Command) set(ctx context.Context, sess *watchsession.Session, specs []string) error {
	changes := make([]settings.Change, 0, len(specs))

	for _, spec := range specs {
		ch, err := settings.ParseChangeSpec(spec)
		if err != nil {
			return err
		}

		changes = append(changes, ch)
	}

	res, err := settings.NewApplyAction(c.Profile).Run(ctx, sess, changes)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, res)
	}

	for _, ch := range res.Changes {
		c.Out.Printf("%s.%s", ch.Mesg, ch.Field)

		if ch.ArrayIndex != nil {
			c.Out.Printf("[%d]", *ch.ArrayIndex)
		}

		c.Out.Printf(": %v -> %v (raw %d)\n", ch.Previous, ch.Value, ch.Raw)
	}

	c.Out.Printf("wrote %s (%d bytes)\n%s\n", res.Path, res.Bytes, res.Note)

	return nil
}
