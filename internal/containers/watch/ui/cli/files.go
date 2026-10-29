package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Devices prints the injected discovery result without opening a Session.
func (c *Commands) Devices(ctx context.Context, _ []string) error {
	devs, err := c.Discover(ctx)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, devs)
	}

	if len(devs) == 0 {
		c.Out.Println("no devices found")
		return nil
	}

	for _, d := range devs {
		c.Out.Printf("%s\t%s\t%s\n", d.Name, d.Manufacturer, d.ID)
	}

	return nil
}

// Info prints the connected watch summary.
func (c *Commands) Info(ctx context.Context, sess *watchsession.Session, _ []string) error {
	sum, err := watch.NewSummaryAction(c.Profile, c.AppCount).Run(ctx, sess)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, sum)
	}

	c.Out.Printf("Device:     %s (%s)\n", sum.Model, sum.Device.Name)
	c.Out.Printf("Part:       %s   Unit ID: %d\n", sum.PartNumber, sum.UnitID)
	c.Out.Printf("Software:   %s\n", sum.Software)
	c.Out.Printf("Apps:       %d installed entries\n", sum.AppCount)
	c.Out.Printf("NewFiles:   %d pending\n", sum.NewFiles)

	if sum.Storage != nil {
		c.Out.Printf("Storage:    %.2f GB free of %.2f GB\n", float64(sum.Storage.FreeBytes)/1e9, float64(sum.Storage.CapacityBytes)/1e9)
	}

	return nil
}

// List formats a directory listing; JSON retains transport order.
func (c *Commands) List(ctx context.Context, sess *watchsession.Session, args []string) error {
	p := ""
	if len(args) > 0 {
		p = args[0]
	}

	entries, err := sess.List(ctx, p)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, entries)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}

		return entries[i].Name < entries[j].Name
	})

	tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)

	for _, e := range entries {
		if e.Dir {
			fmt.Fprintf(tw, "%s/\t\t%s\n", e.Name, e.Modified)
		} else {
			fmt.Fprintf(tw, "%s\t%d\t%s\n", e.Name, e.Size, e.Modified)
		}
	}

	return tw.Flush()
}

// Cat prints text, describes binary data or saves it to a local file.
func (c *Commands) Cat(ctx context.Context, sess *watchsession.Session, args []string) error {
	fs := c.flagSet("cat")
	out := fs.String("o", "", "write to file")

	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return clix.UsageErrorf("usage: cat [-o FILE] PATH")
	}

	data, err := sess.ReadFile(ctx, fs.Arg(0))
	if err != nil {
		return err
	}

	if *out != "" {
		return os.WriteFile(*out, data, 0o600)
	}

	if isText(data) {
		_, err = c.Out.Write(data)
		return err
	}

	c.Out.Printf("%d bytes (binary); use -o FILE to save\n", len(data))

	return nil
}

func isText(b []byte) bool {
	for _, c := range b {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			return false
		}
	}

	return true
}

// Put writes a local file to the supplied watch path.
func (c *Commands) Put(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) != 2 {
		return clix.UsageErrorf("usage: put FILE PATH")
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}

	if err := sess.WriteFile(ctx, args[1], data); err != nil {
		return err
	}

	c.Out.Printf("wrote %d bytes to %s\n", len(data), args[1])

	return nil
}

// Remove deletes one watch path.
func (c *Commands) Remove(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) != 1 {
		return clix.UsageErrorf("usage: rm PATH")
	}

	return sess.DeleteFile(ctx, args[0])
}

// Mkdir creates one watch directory.
func (c *Commands) Mkdir(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) != 1 {
		return clix.UsageErrorf("usage: mkdir PATH")
	}

	return sess.Mkdir(ctx, args[0])
}

// Decode prints FIT records with their profile names.
func (c *Commands) Decode(ctx context.Context, sess *watchsession.Session, args []string) error {
	if len(args) != 1 {
		return clix.UsageErrorf("usage: decode PATH")
	}

	msgs, err := watch.NewDecodeAction(c.Profile).Run(ctx, sess, args[0])
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, msgs)
	}

	for _, m := range msgs {
		name := m.Name
		if name == "" {
			name = fmt.Sprintf("mesg%d", m.Num)
		}

		c.Out.Printf("%s[%d]:", name, m.Index)

		for _, f := range m.Fields {
			if f.Value == nil {
				continue
			}

			fn := f.Name
			if fn == "" {
				fn = fmt.Sprintf("f%d", f.Num)
			}

			c.Out.Printf(" %s=%v", fn, f.Value)
		}

		c.Out.Println()
	}

	return nil
}
