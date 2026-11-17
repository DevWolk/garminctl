// Package cli adapts manual application recovery to command-line input and output.
package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"slices"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

const (
	listCommand           = "list"
	recoveryCommand       = "recovery"
	exportCommand         = "export"
	removePackageCommand  = "remove-package"
	snapshotsCommand      = "snapshots"
	inspectCommand        = "inspect"
	removeSnapshotCommand = "remove-snapshot"

	recoveryUsage = "application recovery list|export CACHE_ID|remove-package CACHE_ID|snapshots|inspect SNAPSHOT_ID|remove-snapshot SNAPSHOT_ID"
)

// Command borrows a lazy app composition and creates no watch or native runtime.
type Command struct {
	Recovery  func() (*application.RecoveryAction, error)
	Snapshots func() (*application.RecoverySnapshotsAction, error)
	// LastInstallation reads the installer helper's record; it starts nothing.
	LastInstallation func(context.Context) (*application.InstallationOutcome, error)
	Out, ErrOut      *clix.Printer
	JSON             bool
}

// Run lists retained metadata, exports one authenticated manual-recovery copy
// or removes one retained entry.
func (c *Command) Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("application", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)

	fs.Usage = func() {
		c.ErrOut.Println("Usage: garminctl [-json] " + statusUsage + " | " + recoveryUsage)
	}
	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	args = fs.Args()
	if len(args) == 1 && args[0] == statusCommand {
		return c.status(ctx)
	}

	if !validRecoveryArgs(args) {
		return clix.UsageErrorf("usage: %s | %s", statusUsage, recoveryUsage)
	}

	if args[1] == snapshotsCommand || args[1] == inspectCommand || args[1] == removeSnapshotCommand {
		return c.snapshots(ctx, args[1:])
	}

	return c.packages(ctx, args)
}

func (c *Command) packages(ctx context.Context, args []string) error {
	if c.Recovery == nil {
		return errors.New("application recovery is not configured")
	}

	action, err := c.Recovery()
	if err != nil {
		return err
	}

	if action == nil {
		return errors.New("application recovery returned no action")
	}

	if args[1] == listCommand {
		return c.list(ctx, action)
	}

	if args[1] == removePackageCommand {
		if err := action.Remove(ctx, args[2]); err != nil {
			return err
		}

		return c.removed("saved package", args[2])
	}

	result, err := action.Export(ctx, args[2])
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, exportResult{Verified: true, Package: recoveryInfo(result.Record), Directory: result.Directory, Path: result.Path, SignaturePath: result.SignaturePath, ManualInstallationRequired: true, DataCompatibility: "not_checked"})
	}

	c.Out.Printf("Verified recovery package for %s (%s/%s, %s).\nPackage: %q\nSignature: %q\n", result.Record.Tag, result.Record.OS, result.Record.Arch, result.Record.Installation, result.Path, result.SignaturePath)
	c.Out.Println("No installation was started and no data was restored. Data compatibility is not checked. Use the OS installer or package manager for manual recovery after stopping the application and its server.")

	return c.Out.Err()
}

func validRecoveryArgs(args []string) bool {
	if len(args) < 2 || args[0] != recoveryCommand {
		return false
	}

	if args[1] == listCommand || args[1] == snapshotsCommand {
		return len(args) == 2
	}

	if !slices.Contains([]string{exportCommand, inspectCommand, removePackageCommand, removeSnapshotCommand}, args[1]) || len(args) != 3 {
		return false
	}

	decoded, err := hex.DecodeString(args[2])

	return err == nil && len(decoded) == 32 && strings.ToLower(args[2]) == args[2]
}

type packageInfo struct {
	CacheID      string `json:"cacheId"`
	Tag          string `json:"tag"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Installation string `json:"installation"`
	Bytes        int64  `json:"bytes"`
	Digest       string `json:"digest"`
	Prerelease   bool   `json:"prerelease"`
}

type exportResult struct {
	Verified                   bool        `json:"verified"`
	Package                    packageInfo `json:"package"`
	Directory                  string      `json:"directory"`
	Path                       string      `json:"path"`
	SignaturePath              string      `json:"signaturePath"`
	ManualInstallationRequired bool        `json:"manualInstallationRequired"`
	DataCompatibility          string      `json:"dataCompatibility"`
}

func recoveryInfo(row application.PackageRecord) packageInfo {
	return packageInfo{CacheID: row.ID, Tag: row.Tag, OS: row.OS, Arch: row.Arch, Installation: row.Installation, Bytes: row.Bytes, Digest: row.Digest, Prerelease: row.Prerelease}
}

func (c *Command) list(ctx context.Context, action *application.RecoveryAction) error {
	rows, unusable, err := action.Catalog(ctx)
	if err != nil {
		return err
	}

	values := make([]packageInfo, 0, len(rows))
	for _, row := range rows {
		values = append(values, recoveryInfo(row))
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, struct {
			Verified bool           `json:"verified"`
			Packages []packageInfo  `json:"packages"`
			Unusable []unusableInfo `json:"unusable"`
		}{Packages: values, Unusable: unusableList(unusable)})
	}

	c.Out.Println("Retained metadata only; export a selected ID to verify its package and signature.")

	for _, row := range values {
		c.Out.Printf("%s  %s  %s/%s  %s\n", row.CacheID, row.Tag, row.OS, row.Arch, row.Installation)
	}

	c.warnUnusable("saved package", removePackageCommand, unusable)

	return c.Out.Err()
}

type unusableInfo struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func unusableList(entries []application.UnusableEntry) []unusableInfo {
	result := make([]unusableInfo, 0, len(entries))
	for _, entry := range entries {
		result = append(result, unusableInfo{ID: entry.ID, Reason: entry.Reason})
	}

	return result
}

// warnUnusable keeps the listing readable by scripts: skipped entries go to
// the diagnostic stream together with the command that deletes them.
func (c *Command) warnUnusable(kind, remove string, entries []application.UnusableEntry) {
	for _, entry := range entries {
		c.ErrOut.Printf("warning: %s %s was skipped: %s\n  remove it with: garminctl application recovery %s %s\n", kind, entry.ID, entry.Reason, remove, entry.ID)
	}
}

func (c *Command) removed(kind, id string) error {
	if c.JSON {
		return clix.PrintJSON(c.Out, struct {
			Removed string `json:"removed"`
		}{Removed: id})
	}

	c.Out.Printf("Removed %s %s.\n", kind, id)

	return c.Out.Err()
}
