// Package cli adapts installed-app operations to command-line syntax and output.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Commands uses supplied writers and leased Sessions without owning lifecycle.
// CacheFor supplies the same current-epoch state used by the backend's adapters.
type Commands struct {
	Out      *clix.Printer
	JSON     bool
	CacheFor func(*watchsession.Session) *apps.SchemaCache
}

// List prints the installed registry.
func (c *Commands) List(ctx context.Context, sess *watchsession.Session, _ []string) error {
	apps, err := (apps.ReadRegistryAction{}).Run(ctx, sess)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, apps)
	}

	tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tNAME\tFILE\tVERSION\tON WATCH\tID")

	for _, a := range apps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", a.TypeName, a.Name, a.FileName, a.Version, onOff(a.Hidden, "hidden", "shown"), a.ID)
	}

	return tw.Flush()
}

// Hide queues a hidden visibility flag.
func (c *Commands) Hide(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.setHidden(ctx, sess, "app-hide", args, true)
}

// Show queues a shown visibility flag.
func (c *Commands) Show(ctx context.Context, sess *watchsession.Session, args []string) error {
	return c.setHidden(ctx, sess, "app-show", args, false)
}

func (c *Commands) setHidden(ctx context.Context, sess *watchsession.Session, cmd string, args []string, hidden bool) error {
	if len(args) != 1 {
		return clix.UsageErrorf("usage: %s APP", cmd)
	}

	backup, err := (apps.VisibilityAction{}).Run(ctx, sess, args[0], hidden)
	if err != nil {
		return err
	}

	c.Out.Printf("queued in GARMIN/Apps/IN.BIN (copy of the registry: %s); the watch applies it when you unplug it\n", backup)

	return nil
}

// Restore stages a saved registry after validating its format.
func (c *Commands) Restore(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) != 1 {
		return clix.UsageErrorf("usage: app-restore FILE   (a saved app registry)")
	}

	b, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}

	if err := (apps.RestoreRegistryAction{}).Run(ctx, sess, args[0], b); err != nil {
		return err
	}

	c.Out.Println("queued as GARMIN/Apps/IN.BIN; the watch restores that app list when you unplug it")

	return nil
}

// Pending prints queued registry changes.
func (c *Commands) Pending(ctx context.Context, sess *watchsession.Session, _ []string) error {
	ch, err := (apps.PendingRegistryAction{}).Run(ctx, sess)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, ch)
	}

	for _, x := range ch {
		c.Out.Printf("%-10s %-28s -> %s\n", x.TypeName, x.Name, onOff(x.Hidden, "hidden", "shown"))
	}

	if len(ch) == 0 {
		c.Out.Println("no app-list changes waiting")
	}

	return nil
}

// Remove deletes app files and prints the existing unplug note.
func (c *Commands) Remove(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) != 1 {
		return clix.UsageErrorf("usage: app-rm APP")
	}

	removed, err := apps.NewRemoveAction(c.CacheFor(sess)).Run(ctx, sess, args[0])
	if err != nil {
		return err
	}

	for _, p := range removed {
		c.Out.Println("deleted", p)
	}

	c.Out.Println("the watch drops the app from its list when you unplug it")

	return nil
}

// Settings reads app settings or dispatches a settings edit.
func (c *Commands) Settings(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) >= 2 && args[0] == "set" {
		return c.setSettings(ctx, sess, args[1], args[2:])
	}

	if len(args) != 1 {
		return clix.UsageErrorf("usage: app-settings APP | app-settings set APP key=value [...]")
	}

	as, err := apps.NewReadSettingsAction(c.CacheFor(sess)).Run(ctx, sess, args[0])
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, as)
	}

	c.Out.Printf("%s (%s, %s)\n", as.App.Name, as.App.TypeName, as.Path)

	if as.SchemaError != "" {
		c.Out.Printf("  (no settings definition: %s)\n", as.SchemaError)
	}

	tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
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

func (c *Commands) setSettings(ctx context.Context, sess *watchsession.Session, app string, kvs []string) error {
	updates := map[string]any{}

	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", kv)
		}

		updates[k] = v
	}

	if len(updates) == 0 {
		return clix.UsageErrorf("usage: app-settings set APP key=value [...]")
	}

	as, err := apps.NewApplySettingsAction(c.CacheFor(sess)).Run(ctx, sess, app, updates)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, as)
	}

	c.Out.Printf("updated %s (%s)\n", as.App.Name, as.Path)

	for _, it := range as.Items {
		if _, changed := updates[it.Key]; !changed {
			continue
		}

		if it.ValueLabel != "" {
			c.Out.Printf("  %s = %v (%s)\n", it.Key, it.Value, it.ValueLabel)
		} else {
			c.Out.Printf("  %s = %v\n", it.Key, it.Value)
		}
	}

	return nil
}

// Schema prints the app settings schema and optional key filter.
func (c *Commands) Schema(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) < 1 {
		return clix.UsageErrorf("usage: app-schema APP [KEY]")
	}

	sc, err := apps.NewSchemaAction(c.CacheFor(sess)).Run(ctx, sess, args[0])
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, sc)
	}

	c.Out.Printf("source: %s\n", sc.Source)

	for _, st := range sc.Settings {
		if len(args) > 1 && st.Key != args[1] {
			continue
		}

		c.Out.Printf("%s  [%s]  %s\n", st.Key, st.Type, st.Label)

		if len(args) > 1 || len(st.Options) <= 6 {
			for _, o := range st.Options {
				c.Out.Printf("    %s = %s\n", o.Value, o.Label)
			}
		} else {
			c.Out.Printf("    %d options (app-schema %s %s to list)\n", len(st.Options), args[0], st.Key)
		}
	}

	return nil
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}

	return no
}
