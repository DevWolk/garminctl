package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/DevWolk/garminctl/internal/garmin"
)

func (c *cli) settings(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) > 0 && args[0] == "set" {
		return c.settingsSet(ctx, svc, args[1:])
	}

	fs := c.flagSet("settings")
	all := fs.Bool("all", false, "include undocumented fields")

	if err := parseFlags(fs, args); err != nil {
		return err
	}

	view, err := svc.Settings(ctx)
	if err != nil {
		return err
	}

	rows := view.Rows()
	if c.asJSON {
		return c.printJSON(rows)
	}

	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
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

func (c *cli) settingsSet(ctx context.Context, svc *garmin.Service, specs []string) error {
	changes := make([]garmin.Change, 0, len(specs))

	for _, spec := range specs {
		ch, err := garmin.ParseChangeSpec(spec)
		if err != nil {
			return err
		}

		changes = append(changes, ch)
	}

	res, err := svc.ApplyChanges(ctx, changes)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(res)
	}

	for _, ch := range res.Changes {
		c.out.Printf("%s.%s", ch.Mesg, ch.Field)

		if ch.ArrayIndex != nil {
			c.out.Printf("[%d]", *ch.ArrayIndex)
		}

		c.out.Printf(": %v -> %v (raw %d)\n", ch.Previous, ch.Value, ch.Raw)
	}

	c.out.Printf("wrote %s (%d bytes)\n%s\n", res.Path, res.Bytes, res.Note)

	return nil
}

func (c *cli) apps(ctx context.Context, svc *garmin.Service, _ []string) error {
	apps, err := svc.Apps(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(apps)
	}

	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tNAME\tFILE\tVERSION\tON WATCH\tID")

	for _, a := range apps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", a.TypeName, a.Name, a.FileName, a.Version, onOff(a.Hidden, "hidden", "shown"), a.ID)
	}

	return tw.Flush()
}

func (c *cli) appHide(ctx context.Context, svc *garmin.Service, args []string) error {
	return c.setAppHidden(ctx, svc, "app-hide", args, true)
}

func (c *cli) appShow(ctx context.Context, svc *garmin.Service, args []string) error {
	return c.setAppHidden(ctx, svc, "app-show", args, false)
}

func (c *cli) setAppHidden(ctx context.Context, svc *garmin.Service, cmd string, args []string, hidden bool) error {
	if len(args) != 1 {
		return usageErrorf("usage: %s APP", cmd)
	}

	backup, err := svc.SetAppHidden(ctx, args[0], hidden)
	if err != nil {
		return err
	}

	c.out.Printf("queued in GARMIN/Apps/IN.BIN (copy of the registry: %s); the watch applies it when you unplug it\n", backup)

	return nil
}

func (c *cli) appRestore(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return usageErrorf("usage: app-restore FILE   (a copy of OUT.BIN from ~/.cache/garminctl)")
	}

	b, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}

	if _, err := garmin.ParseAppsConfig(b); err != nil {
		return fmt.Errorf("%s is not an app registry: %w", args[0], err)
	}

	if err := svc.Device().Write(ctx, garmin.PathAppsIn, b); err != nil {
		return err
	}

	c.out.Println("queued as GARMIN/Apps/IN.BIN; the watch restores that app list when you unplug it")

	return nil
}

func (c *cli) appPending(ctx context.Context, svc *garmin.Service, _ []string) error {
	ch, err := svc.PendingAppChanges(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(ch)
	}

	for _, x := range ch {
		c.out.Printf("%-10s %-28s -> %s\n", x.TypeName, x.Name, onOff(x.Hidden, "hidden", "shown"))
	}

	if len(ch) == 0 {
		c.out.Println("no app-list changes waiting")
	}

	return nil
}

func (c *cli) appRm(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return usageErrorf("usage: app-rm APP")
	}

	removed, err := svc.RemoveApp(ctx, args[0])
	if err != nil {
		return err
	}

	for _, p := range removed {
		c.out.Println("deleted", p)
	}

	c.out.Println("the watch drops the app from its list when you unplug it")

	return nil
}

func (c *cli) appSettings(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) >= 2 && args[0] == "set" {
		return c.appSettingsSet(ctx, svc, args[1], args[2:])
	}

	if len(args) != 1 {
		return usageErrorf("usage: app-settings APP | app-settings set APP key=value [...]")
	}

	as, err := svc.AppSettings(ctx, args[0])
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(as)
	}

	c.out.Printf("%s (%s, %s)\n", as.App.Name, as.App.TypeName, as.Path)

	if as.SchemaError != "" {
		c.out.Printf("  (no settings definition: %s)\n", as.SchemaError)
	}

	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "  KEY\tVALUE\tTYPE\tLABEL")

	for _, it := range as.Items {
		val := fmt.Sprint(it.Value)
		if it.ValueLabel != "" {
			val = fmt.Sprintf("%v = %s", it.Value, it.ValueLabel)
		}

		label := it.Label
		if it.ReadOnly {
			label += " (read-only)"
		}

		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", it.Key, val, it.Type, label)
	}

	return tw.Flush()
}

func (c *cli) appSettingsSet(ctx context.Context, svc *garmin.Service, app string, kvs []string) error {
	updates := map[string]any{}

	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", kv)
		}

		updates[k] = v
	}

	if len(updates) == 0 {
		return usageErrorf("usage: app-settings set APP key=value [...]")
	}

	as, err := svc.SetAppSettings(ctx, app, updates)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(as)
	}

	c.out.Printf("updated %s (%s)\n", as.App.Name, as.Path)

	for _, it := range as.Items {
		if _, changed := updates[it.Key]; !changed {
			continue
		}

		if it.ValueLabel != "" {
			c.out.Printf("  %s = %v (%s)\n", it.Key, it.Value, it.ValueLabel)
		} else {
			c.out.Printf("  %s = %v\n", it.Key, it.Value)
		}
	}

	return nil
}

func (c *cli) appSchema(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) < 1 {
		return usageErrorf("usage: app-schema APP [KEY]")
	}

	sc, err := svc.AppSchema(ctx, args[0])
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(sc)
	}

	c.out.Printf("source: %s\n", sc.Source)

	for _, st := range sc.Settings {
		if len(args) > 1 && st.Key != args[1] {
			continue
		}

		c.out.Printf("%s  [%s]  %s\n", st.Key, st.Type, st.Label)

		if len(args) > 1 || len(st.Options) <= 6 {
			for _, o := range st.Options {
				c.out.Printf("    %s = %s\n", o.Value, o.Label)
			}
		} else {
			c.out.Printf("    %d options (app-schema %s %s to list)\n", len(st.Options), args[0], st.Key)
		}
	}

	return nil
}
