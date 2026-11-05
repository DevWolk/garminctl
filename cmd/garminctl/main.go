// Command garminctl manages Garmin watches over USB and syncs with Garmin Connect.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/devicehost"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

//nolint:dupword // command names repeat at the start of consecutive usage lines
const usage = `garminctl - manage a Garmin watch over USB

Usage: garminctl [global flags] <command> [args]

Global flags:
  -root DIR                       use a mounted watch folder or dump (or set GARMINCTL_ROOT)
  -device ID                      device ID (default: first Garmin device)
  -json                           machine-readable output where applicable

Commands:
  devices                         list connected devices
  info                            show the connected watch
  ls [PATH]                       list a directory on the watch
  cat [-o FILE] PATH              read a file (prints text or writes to FILE)
  put FILE PATH                   write a local file to the watch
  rm PATH                         delete a file on the watch
  mkdir PATH                      create a folder on the watch
  hidden [-write-test] PATH       list hidden files through the Windows MTP bridge
  mtp OPCODE [PARAM...]           read a raw PTP operation through the Windows MTP bridge
  decode PATH                     decode any FIT file on the watch
  settings [-all]                 show watch settings; -all includes undocumented fields
  settings set M.F=V [...]        change settings, e.g. deviceSettings.backlightMode=manual
  apps                            list apps, watch faces, glances and activities
  app-settings APP                show a Connect IQ app's settings (name or file id)
  app-settings set APP K=V [...]  change Connect IQ app settings (V may be an option label)
  app-schema APP [KEY]            show labels and dropdown options of an app's settings
  app-rm APP                      remove a Connect IQ app (finished when the watch is unplugged)
  app-hide APP | app-show APP     hide or show an activity, glance or app on unplug
  app-pending                     show pending app-list changes
  app-restore FILE                restore a saved app list on unplug
  autostart on|off|status         manage the background server at Windows sign-in
  updates                         check Garmin for firmware/data updates and app updates
  updates install PART            download and queue a part number from updates
  software                        installed software components (device.fit)
  cloud login [-email X]          sign in and enter a verification code when prompted
                                  or use GARMIN_EMAIL and GARMIN_PASSWORD for credentials
  cloud mfa CODE                  finish a pending sign-in from cloud login
  cloud status | logout           show account status or sign out
  cloud sync [-no-upload] [-no-download]
                                  upload recorded files and download queued items
  cloud messages                  what Garmin Connect has queued for the watch
  cloud settings                  device settings kept by Garmin Connect
  cloud settings set K=V ...      save settings in Connect and fetch queued changes
  cloud get|post|get1|post1 URL [BODY]
                                  call Connect APIs; get1/post1 use OAuth1
  activities [-n N] [-wait]       list activities; -wait finishes decoding all files
  activity FILE                   one activity with laps and a downsampled track
  health [-days N]                daily health from Garmin Connect
  health day YYYY-MM-DD           intraday heart rate, stress, body battery and sleep stages
  health history [DAYS]           health history for up to a year
  health extras                   fitness metrics, records, badges and weight history
  health weight KG                log a weigh-in in Garmin Connect
  workouts                        workouts stored in Garmin Connect
  workouts create FILE.json [-send] [-local]
                                  save in Connect; -send attempts USB delivery, -local saves only to the watch
  workouts send ID                send an existing Connect workout to the watch
  music ls | music add [-folder Music] FILE... | music rm PATH | music playlist NAME TRACK...
  store search WORDS [-type T] [-sort S]
                                  search the Connect IQ store for this watch
  store list [-type watchface] [-sort mostPopular]
                                  browse by type
  store info APP_ID               details and reviews of one store app
  backup DIR [-skip Music,Podcasts,Audiobooks]
                                  copy the watch's storage to a folder
  desktop                         open the installed native app
  serve [-addr 127.0.0.1:8787] [-autosync=false]
                                  run the web UI, HTTP API and HTTP MCP server
  mcp                             run the MCP server on stdio
  version                         print the version this binary was built from

Exit status: 0 ok, 1 failure, 2 usage error, 130 interrupted.
`

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const cloudCommand = "cloud"

type cli struct {
	runtime *process.Runtime
	epoch   watchsession.ConnectionEpoch
	root    string
	devID   string
	asJSON  bool
	out     *printer // program output
	errOut  *printer // prompts and diagnostics
}

// jobCommand runs after the initial watch lease has been released.
type jobCommand func(ctx context.Context, args []string) error

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

	fs.StringVar(&c.root, "root", os.Getenv("GARMINCTL_ROOT"), "watch folder or dump instead of MTP (default: GARMINCTL_ROOT)")
	fs.StringVar(&c.devID, "device", "", "device ID")
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

	if cmd == cloudCommand && len(rest) > 0 && (rest[0] == "login" || rest[0] == "mfa" || rest[0] == "logout") {
		return c.cloudAccount(ctx, rest) // account commands need no watch
	}

	if fn, ok := c.plainCommands()[cmd]; ok {
		return fn(ctx, rest)
	}

	fn, ok := c.jobCommands()[cmd]
	if !ok && c.sessionCommands()[cmd] == nil {
		fs.Usage()
		return usageErrorf("unknown command %q", cmd)
	}

	return c.runDevice(ctx, cmd, fn, rest)
}

func (c *cli) runDevice(ctx context.Context, cmd string, fn jobCommand, args []string) (err error) {
	runtime := app.NewRuntime(ctx, func(ctx context.Context) (device.Device, error) { return openDevice(ctx, c.root, c.devID) })
	c.runtime = runtime

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()

		err = errors.Join(err, runtime.Close(closeCtx))
	}()

	if job := c.sessionJob(cmd, args); job != nil {
		return c.runSessionJob(runtime.Context(), job, args)
	}

	// cloud sync is a device job; the other cloud commands borrow a session.
	if command := c.sessionCommands()[cmd]; command != nil && (cmd != cloudCommand || !deviceJob(cmd, args)) {
		return c.runSession(runtime.Context(), command, args)
	}

	lease, err := runtime.Manager().Acquire(runtime.Context())
	if err != nil {
		return err
	}

	c.epoch = lease.Epoch()
	if err := lease.Release(); err != nil {
		return err
	}

	return c.runDeviceJob(runtime.Context(), cmd, fn, args)
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

func (c *cli) jobCommands() map[string]jobCommand {
	return map[string]jobCommand{
		cloudCommand: c.cloudSync,
		"activities": c.activities,
		"backup":     c.backup,
	}
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

func deviceJob(cmd string, args []string) bool {
	if cmd == "activities" || cmd == "backup" {
		return true
	}

	if len(args) == 0 {
		return false
	}

	return (cmd == cloudCommand && args[0] == "sync") || (cmd == musicCommand && (args[0] == "add" || args[0] == "rm"))
}

func (c *cli) runDeviceJob(ctx context.Context, cmd string, fn jobCommand, args []string) error {
	if cmd == "activities" || cmd == cloudCommand {
		return fn(ctx, args)
	}

	return c.runBoundJob(ctx, func(ctx context.Context) error { return fn(ctx, args) })
}

// runBoundJob retains admission and connection identity between operation leases.
func (c *cli) runBoundJob(ctx context.Context, body func(context.Context) error) error {
	release, err := c.runtime.Manager().Gate().EnterJob(ctx)
	if err != nil {
		return err
	}
	defer release()

	bound, releaseEpoch, err := c.runtime.Manager().BindEpoch(ctx, c.epoch)
	if err != nil {
		return err
	}
	defer releaseEpoch()

	return body(bound)
}
