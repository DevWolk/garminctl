// Package cli adapts catalog Actions to command-line flags and output.
package cli

import (
	"context"
	"flag"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/store"
	"github.com/DevWolk/garminctl/internal/ship/ciqstore"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Command uses an existing watch lease and its caller-scoped registry reader.
type Command struct {
	Out       *clix.Printer
	ErrOut    *clix.Printer
	JSON      bool
	Installed store.InstalledApps
}

// Run preserves the required watch XML before dispatching store subcommands.
func (c *Command) Run(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) == 0 {
		return clix.UsageErrorf("usage: store search WORDS | store list | store info APP_ID")
	}

	dx, err := sess.DeviceXML(ctx)
	if err != nil {
		return err
	}

	switch args[0] {
	case "info":
		if len(args) < 2 {
			return clix.UsageErrorf("usage: store info APP_ID")
		}

		return c.storeInfo(ctx, dx.PartNumber, args[1])
	case "search", "list":
		return c.storeSearch(ctx, dx.PartNumber, args)
	}

	return clix.UsageErrorf("unknown store command %q", args[0])
}

func (c *Command) storeInfo(ctx context.Context, part, id string) error {
	d, err := store.NewDetailAction(c.Installed).Run(ctx, part, id)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, d)
	}

	c.Out.Printf("%s by %s (%s)\n", d.Name, d.Developer, d.Type)
	c.Out.Printf("version %s (internal %d), %.1f★ from %d reviews, %d downloads, %s\n", d.Version, d.InternalVersion, d.Rating, d.ReviewCount, d.Downloads, ifEmpty(d.Price, "free"))

	if d.Installed {
		c.Out.Printf("installed on the watch: v%d%s\n", d.InstalledVersion, onOff(d.UpdateAvailable, " (update available)", ""))
	}

	c.Out.Println(d.StoreURL)
	c.Out.Println()
	c.Out.Println(d.Description)

	if d.WhatsNew != "" {
		c.Out.Println("\nWhat's new:\n" + d.WhatsNew)
	}

	for _, r := range d.Reviews {
		c.Out.Printf("  %d★ %s: %s\n", r.Rating, r.Reviewer, r.Text)
	}

	return nil
}

func (c *Command) storeSearch(ctx context.Context, part string, args []string) error {
	fs := flag.NewFlagSet("store "+args[0], flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	typ := fs.String("type", "", "watchface, watch-app, widget, datafield, audio-content-provider-app")
	sortBy := fs.String("sort", "", "mostPopular, highestRated, mostRecent, trending")
	page := fs.Int("page", 0, "page number")

	var words []string

	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") {
			break
		}

		words = append(words, a)
	}

	if err := clix.ParseFlags(fs, args[1+len(words):]); err != nil {
		return err
	}

	q := ciqstore.StoreQuery{Keywords: strings.Join(words, " "), Type: *typ, Sort: *sortBy, Page: *page, PageSize: 0} // the store's default page

	view, err := store.NewBrowseAction(c.Installed).Run(ctx, part, q)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, view)
	}

	for _, a := range view.Apps {
		state := ""

		switch {
		case a.UpdateAvailable:
			state = "  [update available]"
		case a.Installed:
			state = "  [installed]"
		}

		c.Out.Printf("%-36s %-24s %-10s %3.1f★(%d) %7d dl  %-10s %s%s\n", a.ID, trunc(a.Name, 24), a.Type, a.Rating, a.ReviewCount, a.Downloads, ifEmpty(a.Price, "free"), a.Developer, state)
	}

	if view.Total >= 0 {
		c.Out.Printf("%d results in total\n", view.Total)
	}

	return nil
}

func ifEmpty(s, d string) string {
	if s == "" {
		return d
	}

	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}

	return no
}
