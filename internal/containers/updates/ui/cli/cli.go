// Package cli adapts watch update Actions to CLI output and exit errors.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Command receives the entry point's leased Session and scoped capabilities.
type Command struct {
	Config      updates.Config
	Installed   updates.InstalledApps
	Invalidated func(uint64)
	Out, ErrOut *clix.Printer
	JSON        bool
}

const updatesUsage = "updates [install PART]"

// Updates preserves check/install dispatch and partial-check exit behavior.
// Arguments are checked first: the check sends the watch's device description
// to the update service, so help or a mistyped command must not start it.
func (c *Command) Updates(ctx context.Context, sess *watchsession.Session, args []string) error {
	fs := flag.NewFlagSet("updates", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)
	fs.Usage = func() { c.ErrOut.Println("Usage: garminctl [-json] " + updatesUsage) }

	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	args = fs.Args()
	if len(args) != 0 && (len(args) != 2 || args[0] != "install") {
		return clix.UsageErrorf("usage: %s", updatesUsage)
	}

	if len(args) == 2 {
		res, err := updates.NewInstallSoftwareAction(c.Config, c.Invalidated).Run(ctx, sess, args[1])
		if err != nil {
			return err
		}

		if c.JSON {
			return clix.PrintJSON(c.Out, res)
		}

		c.Out.Printf("wrote %s (%d bytes)\n%s\n", res.Path, res.Bytes, res.Note)

		return nil
	}

	sw, swErr := updates.NewCheckSoftwareAction(c.Config).Run(ctx, sess)
	apps, appErr := updates.NewCheckAppsAction(c.Installed).Run(ctx)

	return errors.Join(c.reportUpdates(sw, swErr, apps, appErr), ctx.Err())
}

func (c *Command) reportUpdates(sw []updates.SoftwareUpdate, swErr error, apps []updates.AppUpdate, appErr error) error {
	checkErrors, checkErr := updateCheckErrors(swErr, appErr, apps)
	if c.JSON {
		out := map[string]any{"software": sw, "apps": apps}
		if len(checkErrors) > 0 {
			out["errors"] = checkErrors
		}

		return errors.Join(clix.PrintJSON(c.Out, out), checkErr)
	}

	c.Out.Println("Watch software (Garmin update service):")

	switch {
	case swErr != nil:
		c.Out.Println("  error:", swErr)
	case len(sw) == 0:
		c.Out.Println("  no software updates found")
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

		c.Out.Printf("  %-14s %-34s %-8s %-9s %s  %.1f MB -> %s/%s\n", u.PartNumber, u.ProductName, u.Version, kind, state, float64(u.SizeBytes)/1e6, u.InstallPath, u.FileName)
	}

	c.Out.Println("Connect IQ apps:")

	if appErr != nil {
		c.Out.Println("  error:", appErr)
	}

	for _, a := range apps {
		st := "up to date"

		switch {
		case a.UpdateAvailable:
			st = fmt.Sprintf("update available: v%d (%s) at %s", a.LatestVersion, a.LatestName, a.StoreURL)
		case a.Error != "":
			st = a.Error
		}

		c.Out.Printf("  %-20s v%-4d %s\n", a.App.Name, a.InstalledVersion, st)
	}

	return checkErr
}

// Software prints the installed software inventory.
func (c *Command) Software(ctx context.Context, sess *watchsession.Session, _ []string) error {
	parts, err := (updates.InventoryAction{}).Run(ctx, sess)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, parts)
	}

	for _, p := range parts {
		c.Out.Printf("%s\t%.2f\n", p.PartNumber, p.Version)
	}

	return nil
}

func updateCheckErrors(swErr, appErr error, apps []updates.AppUpdate) (map[string]string, error) {
	for _, app := range apps {
		if app.Error != "" {
			appErr = errors.Join(appErr, fmt.Errorf("app %s: %s", app.App.ID, app.Error))
		}
	}

	out := make(map[string]string)

	var checks []error

	if swErr != nil {
		out["software"] = swErr.Error()
		checks = append(checks, fmt.Errorf("software updates: %w", swErr))
	}

	if appErr != nil {
		out["apps"] = appErr.Error()
		checks = append(checks, fmt.Errorf("app updates: %w", appErr))
	}

	return out, errors.Join(checks...)
}
