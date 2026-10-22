// Command garminctl manages a Garmin watch over USB without Garmin Express.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"garminctl/internal/device"
	"garminctl/internal/device/wpd"
	"garminctl/internal/garmin"
	"garminctl/internal/gc"

	"garminctl/internal/mcpserver"
	"garminctl/internal/web"
	"golang.org/x/term"
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
  decode PATH                     decode any FIT file on the watch
  settings [-all]                 show device settings (documented fields; -all includes unknown)
  settings set M.F=V [...]        change settings, e.g. deviceSettings.backlightMode=manual
                                  userProfile.weight=79.5  deviceSettings[0].104[4]=8
  apps                            list installed apps and watch faces
  app-settings APP                show a Connect IQ app's settings (name or file id)
  app-settings set APP K=V [...]  change Connect IQ app settings (V may be an option label)
  app-schema APP [KEY]            show labels and dropdown options of an app's settings
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
	case "decode":
		return c.decode(ctx, svc, rest)
	case "settings":
		return c.settings(ctx, svc, rest)
	case "apps":
		return c.apps(ctx, svc)
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
	fmt.Fprintln(tw, "TYPE\tNAME\tFILE\tVERSION\tENABLED\tID")
	for _, a := range apps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%v\t%s\n", a.TypeName, a.Name, a.FileName, a.Version, a.Enabled, a.ID)
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
	cloud := garmin.NewCloud(svc, gc.New(gc.DefaultStorePath()))
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	return web.Serve(ctx, *addr, func(ctx context.Context) (device.Device, error) {
		return openDevice(ctx, c.root, c.devID)
	})
}

func (c *cli) mcp(ctx context.Context) error {
	return mcpserver.Serve(ctx, func(ctx context.Context) (device.Device, error) {
		return openDevice(ctx, c.root, c.devID)
	})
}
