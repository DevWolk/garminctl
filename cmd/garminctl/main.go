// Command garminctl manages a Garmin watch over USB without Garmin Express.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/devicehost"
	"github.com/DevWolk/garminctl/internal/garmin"
)

//nolint:dupword // command names repeat at the start of consecutive usage lines
const usage = `garminctl - manage a Garmin watch over USB (no Garmin Express needed)

Usage: garminctl [global flags] <command> [args]

Global flags:
  -root DIR      use a directory (mass-storage mount or dump) instead of the MTP watch
  -device ID     portable device id (default: first Garmin device)
  -json          machine-readable output where applicable

Commands:
  devices                         list devices available through the host transport
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
  desktop                        open the installed native app (Windows app from WSL)
  serve [-addr 127.0.0.1:8787]    run the web UI and HTTP API
  mcp                             run the MCP server on stdio
  version                         print the version this binary was built from

Exit status: 0 ok, 1 failure, 2 usage error, 130 interrupted.
`

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

type cli struct {
	root   string
	devID  string
	asJSON bool
	out    *printer // program output
	errOut *printer // prompts and diagnostics
}

// printer writes a command's output and remembers the first write error,
// so commands print freely and run reports a failure once at the end.
type printer struct {
	w   io.Writer
	err error
}

func newPrinter(w io.Writer) *printer { return &printer{w: w} }

func (p *printer) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.note(err)

	return n, err
}

func (p *printer) Printf(format string, a ...any) {
	_, err := fmt.Fprintf(p.w, format, a...)
	p.note(err)
}

func (p *printer) Println(a ...any) {
	_, err := fmt.Fprintln(p.w, a...)
	p.note(err)
}

func (p *printer) Print(a ...any) {
	_, err := fmt.Fprint(p.w, a...)
	p.note(err)
}

func (p *printer) note(err error) {
	if p.err == nil && err != nil {
		p.err = err
	}
}

// Err is the first write error, if any.
func (p *printer) Err() error { return p.err }

// usageError is a mistake in the command line; main exits with 2 for it.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

// exitCode maps the outcome of run to a Unix exit status.
func exitCode(err error) int {
	var ue *usageError

	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ue):
		return 2
	case errors.Is(err, context.Canceled):
		return 130
	}

	return 1
}

// deviceCommand runs against the connected watch.
type deviceCommand func(ctx context.Context, svc *garmin.Service, args []string) error

// plainCommand runs without the watch.
type plainCommand func(ctx context.Context, args []string) error

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)

	code := exitCode(err)
	if code != 0 {
		fmt.Fprintln(os.Stderr, "error:", err)
	}

	os.Exit(code)
}

// run executes one command line, writing program output to stdout and
// prompts and diagnostics to stderr.
func run(args []string, stdout, stderr io.Writer) error {
	c := &cli{out: newPrinter(stdout), errOut: newPrinter(stderr)}

	return errors.Join(c.run(args), c.out.Err())
}

func (c *cli) run(args []string) error {
	fs := c.flagSet("garminctl")
	fs.Usage = func() { c.errOut.Print(usage) }

	fs.StringVar(&c.root, "root", os.Getenv("GARMINCTL_ROOT"), "directory to use instead of the MTP watch")
	fs.StringVar(&c.devID, "device", "", "portable device id")
	fs.BoolVar(&c.asJSON, "json", false, "JSON output")

	if err := parseFlags(fs, args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return usageErrorf("command required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, rest := rest[0], rest[1:]

	if cmd == "cloud" && len(rest) > 0 && (rest[0] == "login" || rest[0] == "mfa" || rest[0] == "logout") {
		return c.cloudAccount(ctx, rest) // account commands need no watch
	}

	if fn, ok := c.plainCommands()[cmd]; ok {
		return fn(ctx, rest)
	}

	fn, ok := c.deviceCommands()[cmd]
	if !ok {
		fs.Usage()
		return usageErrorf("unknown command %q", cmd)
	}

	svc, err := c.open(ctx)
	if err != nil {
		return err
	}

	return errors.Join(fn(ctx, svc, rest), svc.Close())
}

// plainCommands are the commands that do not need the watch.
func (c *cli) plainCommands() map[string]plainCommand {
	return map[string]plainCommand{
		"version":   c.version,
		"devices":   c.devices,
		"serve":     c.serve,
		"desktop":   c.desktop,
		"autostart": c.autostart,
		"mcp":       c.mcp,
	}
}

func (c *cli) version(_ context.Context, _ []string) error {
	c.out.Println("garminctl", version)
	return nil
}

func (c *cli) deviceCommands() map[string]deviceCommand {
	return map[string]deviceCommand{
		"info":         c.info,
		"ls":           c.ls,
		"cat":          c.cat,
		"put":          c.put,
		"rm":           c.rm,
		"mkdir":        c.mkdir,
		"hidden":       c.hidden,
		"mtp":          c.mtp,
		"decode":       c.decode,
		"settings":     c.settings,
		"apps":         c.apps,
		"app-hide":     c.appHide,
		"app-show":     c.appShow,
		"app-restore":  c.appRestore,
		"app-pending":  c.appPending,
		"app-rm":       c.appRm,
		"app-settings": c.appSettings,
		"app-schema":   c.appSchema,
		"updates":      c.updates,
		"software":     c.software,
		"cloud":        c.cloud,
		"store":        c.store,
		"activities":   c.activities,
		"activity":     c.activity,
		"health":       c.health,
		"workouts":     c.workouts,
		"music":        c.music,
		"backup":       c.backup,
	}
}

func (c *cli) open(ctx context.Context) (*garmin.Service, error) {
	dev, err := openDevice(ctx, c.root, c.devID)
	if err != nil {
		return nil, err
	}

	return garmin.New(dev), nil
}

func openDevice(ctx context.Context, root, devID string) (device.Device, error) {
	return devicehost.Open(ctx, root, devID)
}

// flagSet creates a subcommand flag set whose messages go to stderr.
func (c *cli) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.errOut)

	return fs
}

// parseFlags parses args and turns a bad flag into a usage error.
func parseFlags(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		return &usageError{msg: err.Error()}
	}

	return err
}

func (c *cli) printJSON(v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")

	return enc.Encode(v)
}

// flagsFirst moves "-flag" tokens ahead of positional arguments so that
// "create FILE -send" parses like "create -send FILE".
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}

		flags = append(flags, a)

		if takesValue(fs, a) && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}

	return append(flags, pos...)
}

// takesValue reports whether a, as written on the command line, needs the
// next argument as its value: a flag fs knows that is not a boolean and
// carries no "=value". The FlagSet is the one source of what each flag is.
func takesValue(fs *flag.FlagSet, a string) bool {
	name := strings.TrimLeft(a, "-")
	if strings.Contains(name, "=") {
		return false
	}

	f := fs.Lookup(name)
	if f == nil {
		return false
	}

	b, ok := f.Value.(interface{ IsBoolFlag() bool })

	return !ok || !b.IsBoolFlag()
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

// parseScalar turns a CLI value into JSON-ish: true/false, numbers, JSON objects/arrays, else string.
func parseScalar(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}

	if n, err := strconv.ParseFloat(v, 64); err == nil && strconv.FormatFloat(n, 'g', -1, 64) == v {
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
