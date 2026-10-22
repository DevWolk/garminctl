package main

import (
	"context"
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

	if c.asJSON {
		return c.printJSON(map[string]any{"software": sw, "apps": apps})
	}

	c.out.Println("Watch software (Garmin update service):")

	switch {
	case swErr != nil:
		c.out.Println("  error:", swErr)
	case len(sw) == 0:
		c.out.Println("  up to date")
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

	return nil
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
