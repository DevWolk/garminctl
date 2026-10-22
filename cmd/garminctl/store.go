package main

import (
	"context"
	"strings"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/garmin"
)

func (c *cli) store(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: store search WORDS | store list | store info APP_ID")
	}

	dx, err := svc.DeviceXML(ctx)
	if err != nil {
		return err
	}

	switch args[0] {
	case "info":
		if len(args) < 2 {
			return usageErrorf("usage: store info APP_ID")
		}

		return c.storeInfo(ctx, svc, dx.PartNumber, args[1])
	case "search", "list":
		return c.storeSearch(ctx, svc, dx.PartNumber, args)
	}

	return usageErrorf("unknown store command %q", args[0])
}

func (c *cli) storeInfo(ctx context.Context, svc *garmin.Service, part, id string) error {
	d, err := svc.StoreApp(ctx, part, id)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(d)
	}

	c.out.Printf("%s by %s (%s)\n", d.Name, d.Developer, d.Type)
	c.out.Printf("version %s (internal %d), %.1f★ from %d reviews, %d downloads, %s\n", d.Version, d.InternalVersion, d.Rating, d.ReviewCount, d.Downloads, ifEmpty(d.Price, "free"))

	if d.Installed {
		c.out.Printf("installed on the watch: v%d%s\n", d.InstalledVersion, onOff(d.UpdateAvailable, " (update available)", ""))
	}

	c.out.Println(d.StoreURL)
	c.out.Println()
	c.out.Println(d.Description)

	if d.WhatsNew != "" {
		c.out.Println("\nWhat's new:\n" + d.WhatsNew)
	}

	for _, r := range d.Reviews {
		c.out.Printf("  %d★ %s: %s\n", r.Rating, r.Reviewer, r.Text)
	}

	return nil
}

func (c *cli) storeSearch(ctx context.Context, svc *garmin.Service, part string, args []string) error {
	fs := c.flagSet("store " + args[0])
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

	if err := parseFlags(fs, args[1+len(words):]); err != nil {
		return err
	}

	q := ciqstore.StoreQuery{Keywords: strings.Join(words, " "), Type: *typ, Sort: *sortBy, Page: *page, PageSize: 0} // the store's default page

	view, err := svc.BrowseStore(ctx, part, q)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(view)
	}

	for _, a := range view.Apps {
		state := ""

		switch {
		case a.UpdateAvailable:
			state = "  [update available]"
		case a.Installed:
			state = "  [installed]"
		}

		c.out.Printf("%-36s %-24s %-10s %3.1f★(%d) %7d dl  %-10s %s%s\n", a.ID, trunc(a.Name, 24), a.Type, a.Rating, a.ReviewCount, a.Downloads, ifEmpty(a.Price, "free"), a.Developer, state)
	}

	if view.Total >= 0 {
		c.out.Printf("%d results in total\n", view.Total)
	}

	return nil
}
