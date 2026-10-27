package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/DevWolk/garminctl/internal/garmin"
)

func (c *cli) updates(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 2 && args[0] == "install" {
		res, err := svc.InstallSoftwareUpdate(ctx, args[1])
		if err != nil {
			return err
		}

		if c.asJSON {
			return c.printJSON(res)
		}

		c.out.Printf("wrote %s (%d bytes)\n%s\n", res.Path, res.Bytes, res.Note)

		return nil
	}

	sw, swErr := svc.CheckSoftwareUpdates(ctx)
	apps, appErr := svc.CheckAppUpdates(ctx)

	return errors.Join(c.reportUpdates(sw, swErr, apps, appErr), ctx.Err())
}

func (c *cli) reportUpdates(sw []garmin.SoftwareUpdate, swErr error, apps []garmin.AppUpdate, appErr error) error {
	checkErrors, checkErr := updateCheckErrors(swErr, appErr, apps)
	if c.asJSON {
		out := map[string]any{"software": sw, "apps": apps}
		if len(checkErrors) > 0 {
			out["errors"] = checkErrors
		}

		return errors.Join(c.printJSON(out), checkErr)
	}

	c.out.Println("Watch software (Garmin update service):")

	switch {
	case swErr != nil:
		c.out.Println("  error:", swErr)
	case len(sw) == 0:
		c.out.Println("  no software updates found")
	}

	for _, u := range sw {
		kind := u.DataType
		if u.IsPrimaryFirmware {
			kind = "firmware"
		}

		state := onOff(u.IsProductInstalled, "installed", "available")
		c.out.Printf("  %-14s %-34s %-8s %-9s %s  %.1f MB -> %s/%s\n", u.PartNumber, u.ProductName, u.Version, kind, state, float64(u.SizeBytes)/1e6, u.InstallPath, u.FileName)
	}

	c.out.Println("Connect IQ apps:")

	if appErr != nil {
		c.out.Println("  error:", appErr)
	}

	for _, a := range apps {
		st := "up to date"

		switch {
		case a.UpdateAvailable:
			st = fmt.Sprintf("update available: v%d (%s) at %s", a.LatestVersion, a.LatestName, a.StoreURL)
		case a.Error != "":
			st = a.Error
		}

		c.out.Printf("  %-20s v%-4d %s\n", a.App.Name, a.InstalledVersion, st)
	}

	return checkErr
}

func (c *cli) software(ctx context.Context, svc *garmin.Service, _ []string) error {
	parts, err := svc.SoftwareInventory(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(parts)
	}

	for _, p := range parts {
		c.out.Printf("%s\t%.2f\n", p.PartNumber, p.Version)
	}

	return nil
}

func updateCheckErrors(swErr, appErr error, apps []garmin.AppUpdate) (map[string]string, error) {
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
