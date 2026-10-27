package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/device/wpd"
	"github.com/DevWolk/garminctl/internal/devicehost"
	"github.com/DevWolk/garminctl/internal/garmin"
)

func (c *cli) devices(ctx context.Context, _ []string) error {
	devs, err := devicehost.Devices(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(devs)
	}

	if len(devs) == 0 {
		c.out.Println("no devices found")
		return nil
	}

	for _, d := range devs {
		c.out.Printf("%s\t%s\t%s\n", d.Name, d.Manufacturer, d.ID)
	}

	return nil
}

func (c *cli) info(ctx context.Context, svc *garmin.Service, _ []string) error {
	sum, err := svc.Summary(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(sum)
	}

	c.out.Printf("Device:     %s (%s)\n", sum.Model, sum.Device.Name)
	c.out.Printf("Part:       %s   Unit ID: %d\n", sum.PartNumber, sum.UnitID)
	c.out.Printf("Software:   %s\n", sum.Software)
	c.out.Printf("Apps:       %d installed entries\n", sum.AppCount)
	c.out.Printf("NewFiles:   %d pending\n", sum.NewFiles)

	if sum.Storage != nil {
		c.out.Printf("Storage:    %.2f GB free of %.2f GB\n", float64(sum.Storage.FreeBytes)/1e9, float64(sum.Storage.CapacityBytes)/1e9)
	}

	return nil
}

func (c *cli) ls(ctx context.Context, svc *garmin.Service, args []string) error {
	p := ""
	if len(args) > 0 {
		p = args[0]
	}

	entries, err := svc.Device().List(ctx, p)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(entries)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}

		return entries[i].Name < entries[j].Name
	})

	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)

	for _, e := range entries {
		if e.Dir {
			fmt.Fprintf(tw, "%s/\t\t%s\n", e.Name, e.Modified)
		} else {
			fmt.Fprintf(tw, "%s\t%d\t%s\n", e.Name, e.Size, e.Modified)
		}
	}

	return tw.Flush()
}

func (c *cli) cat(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := c.flagSet("cat")
	out := fs.String("o", "", "write to file")

	if err := parseFlags(fs, args); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return usageErrorf("usage: cat [-o FILE] PATH")
	}

	data, err := svc.Device().Read(ctx, fs.Arg(0))
	if err != nil {
		return err
	}

	if *out != "" {
		return os.WriteFile(*out, data, 0o600)
	}

	if isText(data) {
		_, err = c.out.Write(data)
		return err
	}

	c.out.Printf("%d bytes (binary); use -o FILE to save\n", len(data))

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

func (c *cli) put(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 2 {
		return usageErrorf("usage: put FILE PATH")
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}

	if err := svc.Device().Write(ctx, args[1], data); err != nil {
		return err
	}

	c.out.Printf("wrote %d bytes to %s\n", len(data), args[1])

	return nil
}

func (c *cli) rm(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return usageErrorf("usage: rm PATH")
	}

	return svc.Device().Delete(ctx, args[0])
}

func (c *cli) mkdir(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return usageErrorf("usage: mkdir PATH")
	}

	return svc.Device().Mkdir(ctx, args[0])
}

// hidden uses the vendor MTP listing to include hidden folders.
// -write-test writes, reads and deletes a temporary file in the same session.
func (c *cli) hidden(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := c.flagSet("hidden")
	writeTest := fs.Bool("write-test", false, "write and delete a temporary file in the folder")

	if err := parseFlags(fs, flagsFirst(fs, args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return usageErrorf("usage: hidden [-write-test] PATH")
	}

	dev, ok := svc.Device().(*wpd.Device)
	if !ok {
		return errors.New("hidden requires the Windows MTP bridge")
	}

	storage, handle, err := walkHidden(ctx, dev, fs.Arg(0))
	if err != nil {
		return err
	}

	c.out.Printf("%s: storage 0x%08X handle 0x%08X\n", fs.Arg(0), storage, handle)

	kids, err := dev.AllChildren(ctx, storage, handle)
	if err != nil {
		return err
	}

	for _, k := range kids {
		c.out.Printf("  0x%08X  %-24s %s %d\n", k.Handle, k.Name, onOff(k.IsDir(), "dir ", "file"), k.Size)
	}

	if !*writeTest {
		return nil
	}

	return c.hiddenWriteTest(ctx, dev, storage, handle)
}

// walkHidden resolves a watch path to its storage and object handle, passing
// through hidden folders.
func walkHidden(ctx context.Context, dev *wpd.Device, p string) (storage, handle uint32, err error) {
	parts := strings.Split(strings.Trim(p, "/"), "/")

	storage, handle, err = dev.Handles(ctx, "GARMIN")
	if err != nil {
		return 0, 0, err
	}

	if !strings.EqualFold(parts[0], "GARMIN") {
		storage, handle, err = dev.Handles(ctx, parts[0])
		if err != nil {
			return 0, 0, err
		}
	}

	for _, part := range parts[1:] {
		o, found, err := dev.FindChild(ctx, storage, handle, part)
		if err != nil {
			return 0, 0, err
		}

		if !found {
			return 0, 0, fmt.Errorf("%s: no %q under handle 0x%08X", p, part, handle)
		}

		handle = o.Handle
	}

	return storage, handle, nil
}

func (c *cli) hiddenWriteTest(ctx context.Context, dev *wpd.Device, storage, handle uint32) error {
	const name = "GCTLTEST.TMP"

	if old, found, err := dev.FindChild(ctx, storage, handle, name); err == nil && found {
		c.out.Printf("leftover %s (handle 0x%08X, %d bytes) from an earlier run: deleting\n", name, old.Handle, old.Size)

		if err := dev.DeleteHandle(ctx, old.Handle); err != nil {
			return err
		}
	}

	data := []byte(strings.Repeat("garminctl write test\n", 50))

	h, err := dev.SendFile(ctx, storage, handle, name, data)
	if err != nil {
		return fmt.Errorf("write test: %w", err)
	}

	c.out.Printf("wrote %s as handle 0x%08X (%d bytes)\n", name, h, len(data))

	o, found, err := dev.FindChild(ctx, storage, handle, name)
	c.out.Printf("listed after write: found=%v size=%d err=%v\n", found, o.Size, err)

	back, err := dev.ReadHandle(ctx, h)
	c.out.Printf("read back: %d bytes, identical=%v, err=%v\n", len(back), string(back) == string(data), err)

	if err := dev.DeleteHandle(ctx, h); err != nil {
		return fmt.Errorf("delete test file: %w", err)
	}

	_, found, err = dev.FindChild(ctx, storage, handle, name)
	c.out.Printf("deleted; listed after delete: found=%v err=%v\n", found, err)

	return nil
}

// mtp reads a raw PTP operation; opcodes and parameters use hexadecimal.
func (c *cli) mtp(ctx context.Context, svc *garmin.Service, args []string) error {
	dev, ok := svc.Device().(*wpd.Device)
	if !ok || len(args) < 1 {
		return usageErrorf("usage: mtp OPCODE [PARAM...]  (requires the Windows MTP bridge)")
	}

	nums := make([]uint32, 0, len(args))

	for _, a := range args {
		n, err := parseHex32(a)
		if err != nil {
			return err
		}

		nums = append(nums, n)
	}

	data, code, rp, err := dev.MtpRead(ctx, nums[0], nums[1:])
	if err != nil {
		return err
	}

	c.out.Printf("response 0x%04X params %v, %d bytes\n%s", code, rp, len(data), hex.Dump(data))

	return nil
}

// parseHex32 parses a hex number with or without a 0x prefix.
func parseHex32(s string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("%q: %w", s, err)
	}

	return uint32(n), nil
}

func (c *cli) decode(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return usageErrorf("usage: decode PATH")
	}

	msgs, err := svc.DecodeFIT(ctx, args[0])
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(msgs)
	}

	for _, m := range msgs {
		name := m.Name
		if name == "" {
			name = fmt.Sprintf("mesg%d", m.Num)
		}

		c.out.Printf("%s[%d]:", name, m.Index)

		for _, f := range m.Fields {
			if f.Value == nil {
				continue
			}

			fn := f.Name
			if fn == "" {
				fn = fmt.Sprintf("f%d", f.Num)
			}

			c.out.Printf(" %s=%v", fn, f.Value)
		}

		c.out.Println()
	}

	return nil
}

func (c *cli) backup(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := c.flagSet("backup")
	skip := fs.String("skip", "Music,Podcasts,Audiobooks", "comma-separated top-level folders to leave out")

	if err := parseFlags(fs, flagsFirst(fs, args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return usageErrorf("usage: backup DIR")
	}

	dest := fs.Arg(0)
	b := &backupRun{cli: c, svc: svc, skip: map[string]bool{}}

	for s := range strings.SplitSeq(*skip, ",") {
		if s = strings.TrimSpace(s); s != "" {
			b.skip[strings.ToLower(s)] = true
		}
	}

	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}

	// Every file is written through os.Root: a name the watch reports with
	// ".." or a separator cannot land outside DIR.
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()

	b.root = root

	if err := b.walk(ctx, ""); err != nil {
		return err
	}

	c.out.Printf("backed up %d files, %.1f MB, to %s\n", b.files, float64(b.bytes)/1e6, dest)

	return nil
}

// backupRun copies the watch's storage to a folder.
type backupRun struct {
	*cli
	svc   *garmin.Service
	root  *os.Root
	skip  map[string]bool
	files int
	bytes int
}

func (b *backupRun) walk(ctx context.Context, dir string) error {
	var entries []device.Entry

	err := b.withDeviceGroup(ctx, b.svc, func(ctx context.Context, svc *garmin.Service) error {
		var err error

		entries, err = svc.Device().List(ctx, dir)

		return err
	})
	if err != nil {
		return err
	}

	for _, e := range entries {
		p := e.Name
		if dir != "" {
			p = dir + "/" + e.Name
		}

		if dir == "" && b.skip[strings.ToLower(e.Name)] {
			continue
		}

		if e.Dir {
			if err := b.walk(ctx, p); err != nil {
				return err
			}

			continue
		}

		if err := b.copyFile(ctx, p); err != nil {
			return err
		}
	}

	return nil
}

func (b *backupRun) copyFile(ctx context.Context, p string) error {
	var data []byte

	err := b.withDeviceGroup(ctx, b.svc, func(ctx context.Context, svc *garmin.Service) error {
		var err error

		data, err = svc.Device().Read(ctx, p)

		return err
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, device.ErrDisconnected) {
			return err
		}

		b.errOut.Printf("skip %s: %v\n", p, err)

		return nil
	}

	rel := filepath.FromSlash(p)
	if dir := filepath.Dir(rel); dir != "." {
		if err := b.root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}

	if err := b.root.WriteFile(rel, data, 0o600); err != nil {
		return err
	}

	b.files++
	b.bytes += len(data)

	if !b.asJSON {
		b.out.Printf("  %s (%d bytes)\n", p, len(data))
	}

	return nil
}
