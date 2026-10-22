// Command garminctl manages a Garmin watch over USB without Garmin Express.
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"garminctl/internal/ciqstore"
	"garminctl/internal/device"
	"garminctl/internal/device/wpd"
	"garminctl/internal/garmin"
	"garminctl/internal/gc"

	"bytes"
	"garminctl/internal/mcpserver"
	"garminctl/internal/web"
	"golang.org/x/term"
	"io"
	"net/http"
)

const usage = `garminctl - manage a Garmin watch over USB (no Garmin Express needed)

Usage: garminctl [global flags] <command> [args]

Global flags:
  -root DIR      use a directory (mass-storage mount or dump) instead of the MTP watch
  -device ID     portable device id (default: first Garmin device)
  -json          machine-readable output where applicable

Commands:
  devices                         list portable devices Windows sees
  info                            device summary
  ls [PATH]                       list a directory on the watch
  cat [-o FILE] PATH              read a file (prints text or writes to FILE)
  put FILE PATH                   write a local file to the watch
  rm PATH                         delete a file on the watch
  mkdir PATH                      create a folder on the watch
  hidden [-write-test] PATH       list a folder including what MTP hides (the app folder)
  decode PATH                     decode any FIT file on the watch
  settings [-all]                 show device settings (documented fields; -all includes unknown)
  settings set M.F=V [...]        change settings, e.g. deviceSettings.backlightMode=manual
                                  userProfile.weight=79.5  deviceSettings[0].104[4]=8
  apps                            list installed apps and watch faces
  app-settings APP                show a Connect IQ app's settings (name or file id)
  app-settings set APP K=V [...]  change Connect IQ app settings (V may be an option label)
  app-schema APP [KEY]            show labels and dropdown options of an app's settings
  app-rm APP                      remove a Connect IQ app (finished when the watch is unplugged)
  app-hide APP | app-show APP     hide a built-in activity, glance or app from the watch's lists, or show it (applied on unplug)
  app-pending                     app-list changes waiting for the next unplug
  app-restore FILE                queue a saved copy of the app list (from ~/.cache/garminctl) for the watch
  autostart on|off|status         run "garminctl serve" at Windows sign-in, like Garmin Express did
  updates                         check Garmin for firmware/data updates and app updates
  updates install PART            download and write one update (e.g. 006-DA094-00)
  software                        installed software components (device.fit)
  cloud login [-email X]          sign in to Garmin Connect (password prompted; GARMIN_EMAIL/GARMIN_PASSWORD env work too)
  cloud mfa CODE                  finish a sign-in that asked for a verification code
  cloud status | logout           session state / forget the session
  cloud sync [-no-upload] [-no-download]   upload recorded files, pull what Connect queued for the watch
  cloud messages                  what Garmin Connect has queued for the watch
  cloud settings                  device settings kept by Garmin Connect
  cloud settings set K=V ...      change them (Connect then generates the settings file for the watch)
  activities [-n N] [-wait]       activities recorded on the watch (decoded locally, cached)
  activity FILE                   one activity with laps and a downsampled track
  health [-days N]                daily steps, resting HR, stress, body battery, sleep (Garmin Connect)
  health day YYYY-MM-DD           intraday heart rate, stress, body battery and sleep stages
  workouts                        workouts stored in Garmin Connect
  workouts create FILE.json [-send] [-local]   create from a spec (see docs); -send queues it for the watch, -local writes the FIT directly
  workouts send ID                queue an existing Connect workout for the watch
  music ls | music add [-folder Music] FILE... | music rm PATH | music playlist NAME TRACK...
  store search WORDS [-type T] [-sort S]   search the Connect IQ store for this watch
  store list [-type watchface] [-sort mostPopular]   browse by type
  store info APP_ID               details and reviews of one store app
  backup DIR [-skip Music,Podcasts,Audiobooks]   copy the watch's storage to a folder
  serve [-addr 127.0.0.1:8787]    run the web UI and HTTP API
  mcp                             run the MCP server on stdio
`

type cli struct {
	root   string
	devID  string
	asJSON bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("garminctl", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	c := &cli{}
	fs.StringVar(&c.root, "root", os.Getenv("GARMINCTL_ROOT"), "directory to use instead of the MTP watch")
	fs.StringVar(&c.devID, "device", "", "portable device id")
	fs.BoolVar(&c.asJSON, "json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("command required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "devices":
		return c.devices(ctx)
	case "serve":
		return c.serve(ctx, rest)
	case "autostart":
		return c.autostart(rest)
	case "mcp":
		return c.mcp(ctx)
	case "cloud":
		if len(rest) > 0 && (rest[0] == "login" || rest[0] == "mfa" || rest[0] == "logout") {
			return c.cloudAccount(ctx, rest)
		}
	}
	svc, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer svc.Close()
	switch cmd {
	case "info":
		return c.info(ctx, svc)
	case "ls":
		return c.ls(ctx, svc, rest)
	case "cat":
		return c.cat(ctx, svc, rest)
	case "put":
		return c.put(ctx, svc, rest)
	case "rm":
		return c.rm(ctx, svc, rest)
	case "mkdir":
		return c.mkdir(ctx, svc, rest)
	case "hidden":
		return c.hidden(ctx, svc, rest)
	case "mtp": // raw PTP operation with a data phase from the device, for exploring: mtp OPCODE [PARAM...] (hex ok)
		dev, ok := svc.Device().(*wpd.Device)
		if !ok || len(rest) < 1 {
			return errors.New("usage: mtp OPCODE [PARAM...]  (needs the MTP bridge)")
		}
		var nums []uint64
		for _, a := range rest {
			n, err := strconv.ParseUint(strings.TrimPrefix(a, "0x"), 16, 32)
			if err != nil {
				return fmt.Errorf("%q: %w", a, err)
			}
			nums = append(nums, n)
		}
		prms := make([]uint32, 0, len(nums)-1)
		for _, n := range nums[1:] {
			prms = append(prms, uint32(n))
		}
		data, code, rp, err := dev.MtpRead(ctx, uint32(nums[0]), prms)
		if err != nil {
			return err
		}
		fmt.Printf("response 0x%04X params %v, %d bytes\n%s", code, rp, len(data), hex.Dump(data))
		return nil
	case "decode":
		return c.decode(ctx, svc, rest)
	case "settings":
		return c.settings(ctx, svc, rest)
	case "apps":
		return c.apps(ctx, svc)
	case "app-hide", "app-show":
		if len(rest) != 1 {
			return fmt.Errorf("usage: %s APP", cmd)
		}
		backup, err := svc.SetAppHidden(ctx, rest[0], cmd == "app-hide")
		if err != nil {
			return err
		}
		fmt.Printf("queued in GARMIN/Apps/IN.BIN (copy of the registry: %s); the watch applies it when you unplug it\n", backup)
		return nil
	case "app-restore":
		if len(rest) != 1 {
			return errors.New("usage: app-restore FILE   (a copy of OUT.BIN from ~/.cache/garminctl)")
		}
		b, err := os.ReadFile(rest[0])
		if err != nil {
			return err
		}
		if _, err := garmin.ParseAppsConfig(b); err != nil {
			return fmt.Errorf("%s is not an app registry: %w", rest[0], err)
		}
		if err := svc.Device().Write(ctx, garmin.PathAppsIn, b); err != nil {
			return err
		}
		fmt.Println("queued as GARMIN/Apps/IN.BIN; the watch restores that app list when you unplug it")
		return nil
	case "app-pending":
		ch, err := svc.PendingAppChanges(ctx)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(ch)
		}
		for _, x := range ch {
			fmt.Printf("%-10s %-28s -> %s\n", x.TypeName, x.Name, map[bool]string{true: "hidden", false: "shown"}[x.Hidden])
		}
		if len(ch) == 0 {
			fmt.Println("no app-list changes waiting")
		}
		return nil
	case "app-rm":
		if len(rest) != 1 {
			return errors.New("usage: app-rm APP")
		}
		removed, err := svc.RemoveApp(ctx, rest[0])
		if err != nil {
			return err
		}
		for _, p := range removed {
			fmt.Println("deleted", p)
		}
		fmt.Println("the watch drops the app from its list when you unplug it")
		return nil
	case "app-settings":
		return c.appSettings(ctx, svc, rest)
	case "app-schema":
		return c.appSchema(ctx, svc, rest)
	case "updates":
		return c.updates(ctx, svc, rest)
	case "software":
		return c.software(ctx, svc)
	case "cloud":
		return c.cloud(ctx, svc, rest)
	case "store":
		return c.store(ctx, svc, rest)
	case "activities":
		return c.activities(ctx, svc, rest)
	case "activity":
		return c.activity(ctx, svc, rest)
	case "health":
		return c.health(ctx, svc, rest)
	case "workouts":
		return c.workouts(ctx, svc, rest)
	case "music":
		return c.music(ctx, svc, rest)
	case "backup":
		return c.backup(ctx, svc, rest)
	}
	fs.Usage()
	return fmt.Errorf("unknown command %q", cmd)
}

func (c *cli) open(ctx context.Context) (*garmin.Service, error) {
	dev, err := openDevice(ctx, c.root, c.devID)
	if err != nil {
		return nil, err
	}
	return garmin.New(dev), nil
}

func openDevice(ctx context.Context, root, devID string) (device.Device, error) {
	if root != "" {
		return device.NewDirFS(root)
	}
	if !wpd.Available() {
		return nil, errors.New("no MTP bridge on this host (needs Windows or WSL); use -root DIR for a directory")
	}
	octx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return wpd.Open(octx, devID)
}

func (c *cli) printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (c *cli) devices(ctx context.Context) error {
	if !wpd.Available() {
		return errors.New("no MTP bridge on this host")
	}
	devs, err := wpd.Devices(ctx)
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(devs)
	}
	if len(devs) == 0 {
		fmt.Println("no portable devices connected")
		return nil
	}
	for _, d := range devs {
		fmt.Printf("%s\t%s\t%s\n", d.Name, d.Manufacturer, d.ID)
	}
	return nil
}

func (c *cli) info(ctx context.Context, svc *garmin.Service) error {
	sum, err := svc.Summary(ctx)
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(sum)
	}
	fmt.Printf("Device:     %s (%s)\n", sum.Model, sum.Device.Name)
	fmt.Printf("Part:       %s   Unit ID: %d\n", sum.PartNumber, sum.UnitID)
	fmt.Printf("Software:   %s\n", sum.Software)
	fmt.Printf("Apps:       %d installed entries\n", sum.AppCount)
	fmt.Printf("NewFiles:   %d pending\n", sum.NewFiles)
	if sum.Storage != nil {
		fmt.Printf("Storage:    %.2f GB free of %.2f GB\n", float64(sum.Storage.FreeBytes)/1e9, float64(sum.Storage.CapacityBytes)/1e9)
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
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
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
	fs := flag.NewFlagSet("cat", flag.ContinueOnError)
	out := fs.String("o", "", "write to file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cat [-o FILE] PATH")
	}
	data, err := svc.Device().Read(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if *out != "" {
		return os.WriteFile(*out, data, 0o644)
	}
	if isText(data) {
		_, err = os.Stdout.Write(data)
		return err
	}
	fmt.Printf("%d bytes (binary); use -o FILE to save\n", len(data))
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
		return errors.New("usage: put FILE PATH")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	if err := svc.Device().Write(ctx, args[1], data); err != nil {
		return err
	}
	fmt.Printf("wrote %d bytes to %s\n", len(data), args[1])
	return nil
}

func (c *cli) rm(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: rm PATH")
	}
	return svc.Device().Delete(ctx, args[0])
}

// hidden lists a folder through Garmin's vendor MTP operation, which also returns
// what the ordinary listing hides (the app folder). With -write-test it writes a
// small temporary file into the folder and deletes it again in the same session.
func (c *cli) hidden(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := flag.NewFlagSet("hidden", flag.ContinueOnError)
	writeTest := fs.Bool("write-test", false, "write and delete a temporary file in the folder")
	if err := fs.Parse(flagsFirst(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hidden [-write-test] PATH")
	}
	dev, ok := svc.Device().(*wpd.Device)
	if !ok {
		return errors.New("hidden needs the MTP bridge, not -root")
	}
	parts := strings.Split(strings.Trim(fs.Arg(0), "/"), "/")
	storage, handle, err := dev.Handles(ctx, "GARMIN")
	if err != nil {
		return err
	}
	cur := parts[0]
	if !strings.EqualFold(cur, "GARMIN") {
		storage, handle, err = dev.Handles(ctx, cur)
		if err != nil {
			return err
		}
	}
	for _, p := range parts[1:] { // walk through hidden folders too
		o, found, err := dev.FindChild(ctx, storage, handle, p)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%s: no %q under handle 0x%08X", fs.Arg(0), p, handle)
		}
		handle = o.Handle
	}
	fmt.Printf("%s: storage 0x%08X handle 0x%08X\n", fs.Arg(0), storage, handle)
	kids, err := dev.AllChildren(ctx, storage, handle)
	if err != nil {
		return err
	}
	for _, k := range kids {
		fmt.Printf("  0x%08X  %-24s %s %d\n", k.Handle, k.Name, map[bool]string{true: "dir ", false: "file"}[k.IsDir()], k.Size)
	}
	if !*writeTest {
		return nil
	}
	name := "GCTLTEST.TMP"
	if old, found, err := dev.FindChild(ctx, storage, handle, name); err == nil && found {
		fmt.Printf("leftover %s (handle 0x%08X, %d bytes) from an earlier run: deleting\n", name, old.Handle, old.Size)
		if err := dev.DeleteHandle(ctx, old.Handle); err != nil {
			return err
		}
	}
	data := []byte(strings.Repeat("garminctl write test\n", 50))
	h, err := dev.SendFile(ctx, storage, handle, name, data)
	if err != nil {
		return fmt.Errorf("write test: %w", err)
	}
	fmt.Printf("wrote %s as handle 0x%08X (%d bytes)\n", name, h, len(data))
	o, found, err := dev.FindChild(ctx, storage, handle, name)
	fmt.Printf("listed after write: found=%v size=%d err=%v\n", found, o.Size, err)
	back, err := dev.ReadHandle(ctx, h)
	fmt.Printf("read back: %d bytes, identical=%v, err=%v\n", len(back), string(back) == string(data), err)
	if err := dev.DeleteHandle(ctx, h); err != nil {
		return fmt.Errorf("delete test file: %w", err)
	}
	_, found, err = dev.FindChild(ctx, storage, handle, name)
	fmt.Printf("deleted; listed after delete: found=%v err=%v\n", found, err)
	return nil
}

func (c *cli) mkdir(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: mkdir PATH")
	}
	return svc.Device().Mkdir(ctx, args[0])
}

func (c *cli) decode(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: decode PATH")
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
		fmt.Printf("%s[%d]:", name, m.Index)
		for _, f := range m.Fields {
			fn := f.Name
			if fn == "" {
				fn = fmt.Sprintf("f%d", f.Num)
			}
			if f.Value == nil {
				continue
			}
			fmt.Printf(" %s=%v", fn, f.Value)
		}
		fmt.Println()
	}
	return nil
}

func (c *cli) settings(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) > 0 && args[0] == "set" {
		var changes []garmin.Change
		for _, spec := range args[1:] {
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
			fmt.Printf("%s.%s", ch.Mesg, ch.Field)
			if ch.ArrayIndex != nil {
				fmt.Printf("[%d]", *ch.ArrayIndex)
			}
			fmt.Printf(": %v -> %v (raw %d)\n", ch.Previous, ch.Value, ch.Raw)
		}
		fmt.Printf("wrote %s (%d bytes)\n%s\n", res.Path, res.Bytes, res.Note)
		return nil
	}
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	all := fs.Bool("all", false, "include undocumented fields")
	if err := fs.Parse(args); err != nil {
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
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
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

func (c *cli) apps(ctx context.Context, svc *garmin.Service) error {
	apps, err := svc.Apps(ctx)
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(apps)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tNAME\tFILE\tVERSION\tON WATCH\tID")
	for _, a := range apps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", a.TypeName, a.Name, a.FileName, a.Version, map[bool]string{true: "hidden", false: "shown"}[a.Hidden], a.ID)
	}
	return tw.Flush()
}

func (c *cli) appSettings(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) >= 2 && args[0] == "set" {
		updates := map[string]any{}
		for _, kv := range args[2:] {
			eq := strings.Index(kv, "=")
			if eq < 0 {
				return fmt.Errorf("expected key=value, got %q", kv)
			}
			updates[kv[:eq]] = kv[eq+1:]
		}
		if len(updates) == 0 {
			return errors.New("usage: app-settings set APP key=value [...]")
		}
		as, err := svc.SetAppSettings(ctx, args[1], updates)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(as)
		}
		fmt.Printf("updated %s (%s)\n", as.App.Name, as.Path)
		for _, it := range as.Items {
			if _, changed := updates[it.Key]; !changed {
				continue
			}
			if it.ValueLabel != "" {
				fmt.Printf("  %s = %v (%s)\n", it.Key, it.Value, it.ValueLabel)
			} else {
				fmt.Printf("  %s = %v\n", it.Key, it.Value)
			}
		}
		return nil
	}
	if len(args) != 1 {
		return errors.New("usage: app-settings APP | app-settings set APP key=value [...]")
	}
	as, err := svc.AppSettings(ctx, args[0])
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(as)
	}
	fmt.Printf("%s (%s, %s)\n", as.App.Name, as.App.TypeName, as.Path)
	if as.SchemaError != "" {
		fmt.Printf("  (no settings definition: %s)\n", as.SchemaError)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
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

func (c *cli) appSchema(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: app-schema APP [KEY]")
	}
	sc, err := svc.AppSchema(ctx, args[0])
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(sc)
	}
	fmt.Printf("source: %s\n", sc.Source)
	for _, st := range sc.Settings {
		if len(args) > 1 && st.Key != args[1] {
			continue
		}
		fmt.Printf("%s  [%s]  %s\n", st.Key, st.Type, st.Label)
		if len(args) > 1 || len(st.Options) <= 6 {
			for _, o := range st.Options {
				fmt.Printf("    %s = %s\n", o.Value, o.Label)
			}
		} else {
			fmt.Printf("    %d options (app-schema %s %s to list)\n", len(st.Options), args[0], st.Key)
		}
	}
	return nil
}

func (c *cli) updates(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 2 && args[0] == "install" {
		res, err := svc.InstallSoftwareUpdate(ctx, args[1])
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(res)
		}
		fmt.Printf("wrote %s (%d bytes)\n%s\n", res.Path, res.Bytes, res.Note)
		return nil
	}
	sw, swErr := svc.CheckSoftwareUpdates(ctx)
	apps, appErr := svc.CheckAppUpdates(ctx)
	if c.asJSON {
		return c.printJSON(map[string]any{"software": sw, "apps": apps})
	}
	fmt.Println("Watch software (Garmin update service):")
	if swErr != nil {
		fmt.Println("  error:", swErr)
	} else if len(sw) == 0 {
		fmt.Println("  up to date")
	}
	for _, u := range sw {
		kind := u.DataType
		if u.IsPrimaryFirmware {
			kind = "firmware"
		}
		state := "available"
		if u.IsProductInstalled {
			state = "installed"
		}
		fmt.Printf("  %-14s %-34s %-8s %-9s %s  %.1f MB -> %s/%s\n", u.PartNumber, u.ProductName, u.Version, kind, state, float64(u.SizeBytes)/1e6, u.InstallPath, u.FileName)
	}
	fmt.Println("Connect IQ apps:")
	if appErr != nil {
		fmt.Println("  error:", appErr)
	}
	for _, a := range apps {
		st := "up to date"
		if a.UpdateAvailable {
			st = fmt.Sprintf("update available: v%d (%s) at %s", a.LatestVersion, a.LatestName, a.StoreURL)
		} else if a.Error != "" {
			st = a.Error
		}
		fmt.Printf("  %-20s v%-4d %s\n", a.App.Name, a.InstalledVersion, st)
	}
	return nil
}

func (c *cli) software(ctx context.Context, svc *garmin.Service) error {
	parts, err := svc.SoftwareInventory(ctx)
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(parts)
	}
	for _, p := range parts {
		fmt.Printf("%s\t%.2f\n", p.PartNumber, p.Version)
	}
	return nil
}

func (c *cli) activities(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := flag.NewFlagSet("activities", flag.ContinueOnError)
	n := fs.Int("n", 30, "how many to show")
	wait := fs.Bool("wait", false, "decode every file before answering")
	if err := fs.Parse(flagsFirst(args)); err != nil {
		return err
	}
	list, err := svc.Activities(ctx, *wait)
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
		fmt.Printf("%-28s %-16s %-10s %7.2f km %8s %4d kcal  HR %3d/%3d\n", a.File, a.Start.Local().Format("2006-01-02 15:04"), a.Sport, a.Distance/1000, fmtDur(a.Timer), a.Calories, a.AvgHR, a.MaxHR)
	}
	if list.Pending > 0 {
		fmt.Printf("%d of %d files still being decoded in the background; run again (or use -wait)\n", list.Pending, list.Total)
	}
	return nil
}

// flagsFirst moves "-flag" tokens ahead of positional arguments so that
// "create FILE -send" parses like "create -send FILE".
func flagsFirst(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && isValueFlag(a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

// isValueFlag names the flags that take a value (all others are booleans).
func isValueFlag(a string) bool {
	switch strings.TrimLeft(a, "-") {
	case "n", "days", "type", "sort", "page", "folder", "skip", "email", "addr", "o", "device", "root":
		return true
	}
	return false
}

func fmtDur(sec float64) string {
	d := time.Duration(sec) * time.Second
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func (c *cli) activity(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: activity FILE")
	}
	d, err := svc.Activity(ctx, args[0], 1500)
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(d)
	}
	fmt.Printf("%s %s %s: %.2f km in %s, %d kcal, HR %d avg / %d max, %d m up, %d points, %d laps\n", d.File, d.Start.Local().Format("2006-01-02 15:04"), d.Sport, d.Distance/1000, fmtDur(d.Timer), d.Calories, d.AvgHR, d.MaxHR, d.Ascent, len(d.Points), len(d.Laps))
	for i, l := range d.Laps {
		fmt.Printf("  lap %2d  %8s  %6.2f km  HR %3d\n", i+1, fmtDur(l.Elapsed), l.Distance/1000, l.AvgHR)
	}
	return nil
}

func (c *cli) health(ctx context.Context, svc *garmin.Service, args []string) error {
	cloud := garmin.NewCloud(svc, gc.New(gc.DefaultStorePath()))
	if len(args) >= 1 && args[0] == "extras" {
		x, err := cloud.Extras(ctx)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(x)
		}
		if x.VO2Max > 0 {
			fmt.Printf("VO2 max %.1f (%s)\n", x.VO2Max, x.VO2MaxDate)
		}
		if x.FitnessAge > 0 {
			fmt.Printf("fitness age %.0f (chronological %d)\n", x.FitnessAge, x.ChronologicalA)
		}
		if x.HRV != nil {
			fmt.Printf("HRV last night %d ms, weekly %d ms, %s\n", x.HRV.LastNight, x.HRV.WeeklyAvg, x.HRV.Status)
		}
		for _, r := range x.Records {
			fmt.Printf("record  %-24s %-12s %s\n", r.Label, r.Display, r.Date)
		}
		for i, b := range x.Badges {
			if i >= 10 {
				fmt.Printf("… %d more badges\n", len(x.Badges)-10)
				break
			}
			fmt.Printf("badge   %-32s %s (%d pt)\n", b.Name, b.Date, b.Points)
		}
		for _, w := range x.Weight {
			fmt.Printf("weight  %s %.1f kg\n", w.Date, w.Kg)
		}
		for _, e := range x.Errors {
			fmt.Println("error:", e)
		}
		return nil
	}
	if len(args) >= 2 && args[0] == "weight" {
		kg, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			return fmt.Errorf("weight: %w", err)
		}
		if err := cloud.LogWeight(ctx, kg, time.Time{}); err != nil {
			return err
		}
		fmt.Printf("logged %.1f kg in Garmin Connect\n", kg)
		return nil
	}
	if len(args) >= 1 && args[0] == "history" {
		n := 90
		if len(args) >= 2 {
			n, _ = strconv.Atoi(args[1])
		}
		rows, err := cloud.History(ctx, n)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(rows)
		}
		for _, r := range rows {
			fmt.Printf("%s steps %6d  rhr %3d  stress %3d  bb %3d-%3d  sleep %s\n", r.Date, r.Steps, r.RestingHR, r.AvgStress, r.BodyBatteryLow, r.BodyBatteryHigh, fmtDur(float64(r.SleepSec)))
		}
		return nil
	}
	if len(args) >= 2 && args[0] == "day" {
		d, err := cloud.Day(ctx, args[1])
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(d)
		}
		fmt.Printf("%s: %d heart-rate samples, %d stress samples, %d body-battery samples\n", d.Date, len(d.HeartRate), len(d.Stress), len(d.BodyBattery))
		if d.Sleep != nil {
			fmt.Printf("sleep: deep %s, light %s, REM %s, awake %s, score %d %s\n", fmtDur(float64(d.Sleep.DeepSec)), fmtDur(float64(d.Sleep.LightSec)), fmtDur(float64(d.Sleep.RemSec)), fmtDur(float64(d.Sleep.AwakeSec)), d.Sleep.Score, d.Sleep.Quality)
		}
		for _, e := range d.Errors {
			fmt.Println("warning:", e)
		}
		return nil
	}
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	days := fs.Int("days", 14, "days to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rows, err := cloud.Daily(ctx, *days)
	if err != nil {
		return err
	}
	if c.asJSON {
		return c.printJSON(rows)
	}
	fmt.Printf("%-10s %6s %5s %4s %6s %8s %7s %5s\n", "date", "steps", "goal", "rHR", "stress", "battery", "sleep", "kcal")
	for _, d := range rows {
		if d.Error != "" {
			fmt.Printf("%-10s %s\n", d.Date, d.Error)
			continue
		}
		fmt.Printf("%-10s %6d %5d %4d %6d %3d-%-3d %7s %5d\n", d.Date, d.Steps, d.StepGoal, d.RestingHR, d.AvgStress, d.BodyBatteryLow, d.BodyBatteryHigh, fmtDur(float64(d.SleepSec)), d.Calories)
	}
	return nil
}

func (c *cli) workouts(ctx context.Context, svc *garmin.Service, args []string) error {
	cloud := garmin.NewCloud(svc, gc.New(gc.DefaultStorePath()))
	if len(args) == 0 {
		list, err := cloud.Workouts(ctx, 50)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(list)
		}
		for _, w := range list {
			fmt.Printf("%-12d %-32s %-10s %8s %s\n", w.ID, trunc(w.Name, 32), w.Sport, fmtDur(float64(w.Duration)), w.Updated)
		}
		return nil
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("workouts create", flag.ContinueOnError)
		send := fs.Bool("send", false, "queue it for the watch through Garmin Connect")
		local := fs.Bool("local", false, "write the FIT straight into GARMIN/NewFiles instead of Garmin Connect")
		if err := fs.Parse(flagsFirst(args[1:])); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: workouts create FILE.json [-send] [-local]")
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
			fmt.Println("written to", p, "- unplug the watch; the workout appears under Training > Workouts")
			return nil
		}
		wk, err := cloud.CreateWorkout(ctx, &spec)
		if err != nil {
			return err
		}
		fmt.Printf("saved in Garmin Connect as workout %d\n", wk.ID)
		if *send {
			pulled, err := cloud.SendWorkout(ctx, wk.ID, wk.Name)
			if err != nil {
				return err
			}
			for _, p := range pulled {
				fmt.Println("written to the watch:", p.Path)
			}
			if len(pulled) == 0 {
				fmt.Println("queued for the watch; sync later")
			}
		}
		return nil
	case "send":
		if len(args) < 2 {
			return errors.New("usage: workouts send ID")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}
		pulled, err := cloud.SendWorkout(ctx, id, "")
		if err != nil {
			return err
		}
		for _, p := range pulled {
			fmt.Println("written to the watch:", p.Path)
		}
		if len(pulled) == 0 {
			fmt.Println("queued for the watch; sync later")
		}
		return nil
	}
	return fmt.Errorf("unknown workouts command %q", args[0])
}

func (c *cli) music(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	switch args[0] {
	case "ls":
		lib, err := svc.Music(ctx)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(lib)
		}
		for _, t := range lib.Tracks {
			fmt.Printf("%-60s %6.1f MB\n", t.Path, float64(t.Size)/1e6)
		}
		fmt.Printf("%d tracks, %.1f MB\n", len(lib.Tracks), float64(lib.Bytes)/1e6)
		for _, p := range lib.Playlists {
			fmt.Printf("playlist %s: %d tracks\n", p.Name, len(p.Tracks))
		}
		for _, p := range lib.Providers {
			fmt.Printf("%s v%d keeps %.1f MB of downloads in its protected cache (hidden from USB)\n", p.Name, p.Version, float64(p.CacheBytes)/1e6)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("music add", flag.ContinueOnError)
		folder := fs.String("folder", "Music", "Music, Podcasts or Audiobooks")
		if err := fs.Parse(flagsFirst(args[1:])); err != nil {
			return err
		}
		for _, f := range fs.Args() {
			data, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			t, err := svc.AddTrack(ctx, *folder, filepath.Base(f), data)
			if err != nil {
				return err
			}
			fmt.Println("added", t.Path)
		}
		return nil
	case "rm":
		for _, p := range args[1:] {
			if err := svc.RemoveTrack(ctx, p); err != nil {
				return err
			}
			fmt.Println("removed", p)
		}
		return nil
	case "playlist":
		if len(args) < 3 {
			return errors.New("usage: music playlist NAME TRACK...")
		}
		pl, err := svc.WritePlaylist(ctx, args[1], args[2:])
		if err != nil {
			return err
		}
		fmt.Println("written", pl.Path)
		return nil
	}
	return fmt.Errorf("unknown music command %q", args[0])
}

func (c *cli) store(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: store search WORDS | store list | store info APP_ID")
	}
	dx, err := svc.DeviceXML(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "info":
		if len(args) < 2 {
			return errors.New("usage: store info APP_ID")
		}
		d, err := svc.StoreApp(ctx, dx.PartNumber, args[1])
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(d)
		}
		fmt.Printf("%s by %s (%s)\n", d.Name, d.Developer, d.Type)
		fmt.Printf("version %s (internal %d), %.1f★ from %d reviews, %d downloads, %s\n", d.Version, d.InternalVersion, d.Rating, d.ReviewCount, d.Downloads, ifEmpty(d.Price, "free"))
		if d.Installed {
			fmt.Printf("installed on the watch: v%d%s\n", d.InstalledVersion, map[bool]string{true: " (update available)", false: ""}[d.UpdateAvailable])
		}
		fmt.Println(d.StoreURL)
		fmt.Println()
		fmt.Println(d.Description)
		if d.WhatsNew != "" {
			fmt.Println("\nWhat's new:\n" + d.WhatsNew)
		}
		for _, r := range d.Reviews {
			fmt.Printf("  %d★ %s: %s\n", r.Rating, r.Reviewer, r.Text)
		}
		return nil
	case "search", "list":
		fs := flag.NewFlagSet("store "+args[0], flag.ContinueOnError)
		typ := fs.String("type", "", "watchface, watch-app, widget, datafield, audio-content-provider-app")
		sort := fs.String("sort", "", "mostPopular, highestRated, mostRecent, trending")
		page := fs.Int("page", 0, "page number")
		var words []string
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "-") {
				break
			}
			words = append(words, a)
		}
		if err := fs.Parse(args[1+len(words):]); err != nil {
			return err
		}
		view, err := svc.BrowseStore(ctx, dx.PartNumber, ciqstore.StoreQuery{Keywords: strings.Join(words, " "), Type: *typ, Sort: *sort, Page: *page, PageSize: 24})
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(view)
		}
		for _, a := range view.Apps {
			state := ""
			if a.UpdateAvailable {
				state = "  [update available]"
			} else if a.Installed {
				state = "  [installed]"
			}
			fmt.Printf("%-36s %-24s %-10s %3.1f★(%d) %7d dl  %-10s %s%s\n", a.ID, trunc(a.Name, 24), a.Type, a.Rating, a.ReviewCount, a.Downloads, ifEmpty(a.Price, "free"), a.Developer, state)
		}
		if view.Total >= 0 {
			fmt.Printf("%d results in total\n", view.Total)
		}
		return nil
	}
	return fmt.Errorf("unknown store command %q", args[0])
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

func (c *cli) backup(ctx context.Context, svc *garmin.Service, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	skip := fs.String("skip", "Music,Podcasts,Audiobooks", "comma-separated top-level folders to leave out")
	if err := fs.Parse(flagsFirst(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: backup DIR")
	}
	dest := fs.Arg(0)
	skipSet := map[string]bool{}
	for _, s := range strings.Split(*skip, ",") {
		if s = strings.TrimSpace(s); s != "" {
			skipSet[strings.ToLower(s)] = true
		}
	}
	files, bytes := 0, 0
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := svc.Device().List(ctx, dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			p := e.Name
			if dir != "" {
				p = dir + "/" + e.Name
			}
			if dir == "" && skipSet[strings.ToLower(e.Name)] {
				continue
			}
			if e.Dir {
				if err := walk(p); err != nil {
					return err
				}
				continue
			}
			data, err := svc.Device().Read(ctx, p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "skip %s: %v\n", p, err)
				continue
			}
			out := filepath.Join(dest, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return err
			}
			files++
			bytes += len(data)
			if !c.asJSON {
				fmt.Printf("  %s (%d bytes)\n", p, len(data))
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return err
	}
	fmt.Printf("backed up %d files, %.1f MB, to %s\n", files, float64(bytes)/1e6, dest)
	return nil
}

func (c *cli) cloudAccount(ctx context.Context, args []string) error {
	client := gc.New(gc.DefaultStorePath())
	switch args[0] {
	case "logout":
		if err := client.Logout(); err != nil {
			return err
		}
		fmt.Println("signed out")
		return nil
	case "mfa":
		if len(args) < 2 {
			return errors.New("usage: cloud mfa CODE")
		}
		if err := client.ResumeMFA(ctx, args[1]); err != nil {
			return err
		}
		fmt.Println("signed in as", client.Email())
		return nil
	}
	fs := flag.NewFlagSet("cloud login", flag.ContinueOnError)
	email := fs.String("email", os.Getenv("GARMIN_EMAIL"), "Garmin account email")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *email == "" {
		fmt.Print("Garmin account email: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		*email = strings.TrimSpace(line)
	}
	password := os.Getenv("GARMIN_PASSWORD")
	if password == "" {
		fmt.Print("Password: ")
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			password = string(b)
		} else {
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			password = strings.TrimRight(line, "\r\n")
		}
	}
	res, err := client.Login(ctx, *email, password)
	if err != nil {
		return err
	}
	if res.NeedsMFA {
		fmt.Printf("Garmin sent a verification code by %s. Enter it: ", res.MFAMethod)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if err := client.ResumeMFA(ctx, strings.TrimSpace(line)); err != nil {
			return err
		}
	}
	fmt.Println("signed in as", client.Email())
	return nil
}

func (c *cli) cloud(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cloud status|sync|messages|settings")
	}
	cl := gc.New(gc.DefaultStorePath())
	cloud := garmin.NewCloud(svc, cl)
	if len(args) > 1 && (args[0] == "get" || args[0] == "post" || args[0] == "get1" || args[0] == "post1") {
		// Raw Connect calls for exploring the API: get/post use the bearer token on connectapi
		// paths; get1/post1 sign an absolute URL with OAuth1 like Garmin Express.
		method, body, ct := http.MethodGet, []byte(nil), ""
		if strings.HasPrefix(args[0], "post") {
			method, ct = http.MethodPost, "application/json"
			if len(args) > 2 {
				body = []byte(args[2])
			}
		}
		var st int
		var b []byte
		var err error
		if strings.HasSuffix(args[0], "1") {
			st, b, err = cl.DoOAuth1(ctx, method, args[1], ct, body)
		} else {
			var r io.Reader
			if body != nil {
				r = bytes.NewReader(body)
			}
			st, b, err = cl.Do(ctx, method, args[1], ct, r)
		}
		if err != nil {
			return err
		}
		fmt.Printf("HTTP %d\n", st)
		var pretty bytes.Buffer
		if json.Indent(&pretty, b, "", " ") == nil {
			os.Stdout.Write(pretty.Bytes())
		} else {
			os.Stdout.Write(b)
		}
		fmt.Println()
		return nil
	}
	switch args[0] {
	case "status":
		st := cloud.Status(ctx)
		if c.asJSON {
			return c.printJSON(st)
		}
		if !st.LoggedIn {
			fmt.Println("not signed in (garminctl cloud login)")
			return nil
		}
		name := st.Email
		if st.Profile != nil && st.Profile.Name != "" {
			name = st.Profile.Name + " <" + st.Email + ">"
		}
		fmt.Println("signed in as", name)
		if st.Registered {
			fmt.Printf("watch registered on the account as %q\n", st.DeviceName)
		} else {
			fmt.Println("watch is not registered on this account")
		}
		if st.Error != "" {
			fmt.Println("warning:", st.Error)
		}
		return nil
	case "sync":
		fs := flag.NewFlagSet("cloud sync", flag.ContinueOnError)
		noUp := fs.Bool("no-upload", false, "skip uploading recorded files")
		noDown := fs.Bool("no-download", false, "skip pulling queued messages")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		res, err := cloud.Sync(ctx, !*noUp, !*noDown, func(line string) {
			if !c.asJSON {
				fmt.Println(" ", line)
			}
		})
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(res)
		}
		if res.Upload != nil {
			fmt.Printf("uploaded %d, already in Connect %d, failed %d, unchanged %d\n", res.Upload.Uploaded, res.Upload.Duplicate, res.Upload.Failed, res.Upload.Skipped)
		}
		fmt.Printf("pulled %d item(s) from Connect\n", len(res.Pulled))
		for _, e := range res.Errors {
			fmt.Println("error:", e)
		}
		return nil
	case "messages":
		msgs, err := cloud.Messages(ctx)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(msgs)
		}
		if len(msgs) == 0 {
			fmt.Println("nothing queued")
		}
		for _, m := range msgs {
			fmt.Printf("%-10d %-18s %-8s %s\n", m.MessageID, m.MessageType, m.MessageStatus, m.MetaData.MessageName)
		}
		return nil
	case "settings":
		if len(args) >= 2 && args[1] == "set" {
			changes := map[string]any{}
			for _, kv := range args[2:] {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("expected key=value, got %q", kv)
				}
				changes[k] = parseScalar(v)
			}
			res, err := cloud.SetSettings(ctx, changes)
			if err != nil {
				return err
			}
			if c.asJSON {
				return c.printJSON(res)
			}
			fmt.Println(res.Note)
			return nil
		}
		items, err := cloud.Settings(ctx)
		if err != nil {
			return err
		}
		if c.asJSON {
			return c.printJSON(items)
		}
		for _, it := range items {
			v, _ := json.Marshal(it.Value)
			fmt.Printf("%-28s %s\n", it.Key, v)
		}
		return nil
	}
	return fmt.Errorf("unknown cloud command %q", args[0])
}

// parseScalar turns a CLI value into JSON-ish: true/false, numbers, JSON objects/arrays, else string.
func parseScalar(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	var n float64
	if _, err := fmt.Sscanf(v, "%g", &n); err == nil && fmt.Sprint(n) == v {
		return n
	}
	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		var j any
		if json.Unmarshal([]byte(v), &j) == nil {
			return j
		}
	}
	return v
}

func (c *cli) serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8787", "listen address")
	autoSync := fs.Bool("autosync", true, "watch USB; when the watch appears, sync with Garmin Connect, check updates and notify")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return web.Serve(ctx, *addr, func(ctx context.Context) (device.Device, error) {
		return openDevice(ctx, c.root, c.devID)
	}, *autoSync && c.root == "")
}

func (c *cli) mcp(ctx context.Context) error {
	return mcpserver.Serve(ctx, func(ctx context.Context) (device.Device, error) {
		return openDevice(ctx, c.root, c.devID)
	})
}
