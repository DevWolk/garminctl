// Package cli adapts manual application recovery to command-line input and output.
package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

const (
	listCommand      = "list"
	recoveryCommand  = "recovery"
	exportCommand    = "export"
	snapshotsCommand = "snapshots"
	inspectCommand   = "inspect"
)

// Command borrows a lazy app composition and creates no watch or native runtime.
type Command struct {
	Recovery    func() (*application.RecoveryAction, error)
	Snapshots   func() (*application.RecoverySnapshotsAction, error)
	Out, ErrOut *clix.Printer
	JSON        bool
}

// Run lists retained metadata or exports one authenticated manual-recovery copy.
func (c *Command) Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("application", flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)

	fs.Usage = func() {
		c.ErrOut.Println("Usage: garminctl [-json] application recovery list|export CACHE_ID|snapshots|inspect SNAPSHOT_ID")
	}
	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	args = fs.Args()
	if !validRecoveryArgs(args) {
		return clix.UsageErrorf("usage: application recovery list|export CACHE_ID|snapshots|inspect SNAPSHOT_ID")
	}

	if args[1] == snapshotsCommand || args[1] == inspectCommand {
		return c.snapshots(ctx, args[1:])
	}

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

	if (args[1] != exportCommand && args[1] != inspectCommand) || len(args) != 3 {
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
	rows, err := action.List(ctx)
	if err != nil {
		return err
	}

	values := make([]packageInfo, 0, len(rows))
	for _, row := range rows {
		values = append(values, recoveryInfo(row))
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, struct {
			Verified bool          `json:"verified"`
			Packages []packageInfo `json:"packages"`
		}{Packages: values})
	}

	c.Out.Println("Retained metadata only; export a selected ID to verify its package and signature.")

	for _, row := range values {
		c.Out.Printf("%s  %s  %s/%s  %s\n", row.CacheID, row.Tag, row.OS, row.Arch, row.Installation)
	}

	return c.Out.Err()
}
