package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/device/wpd"
	"github.com/DevWolk/garminctl/internal/ship/hostexec"
)

// desktop opens the installed native app; WSL launches the Windows installation.
// The global -root and -device options reach the app, which then starts its own
// server instead of reusing one that may serve another watch.
func (c *cli) desktop(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return clix.UsageErrorf("usage: garminctl [-root DIR] [-device ID] desktop")
	}

	if ps := wpd.PowerShellPath(); ps != "" {
		appArgs, err := c.desktopArgs(ctx, windowsPath)
		if err != nil {
			return err
		}

		cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", desktopLaunchScript(appArgs)) //nolint:gosec // fixed script with quoted arguments, PowerShell found by the existing WPD platform helper.
		hostexec.HideConsole(cmd)
		cmd.Stdout, cmd.Stderr = c.out, c.errOut

		return cmd.Run()
	}

	app, err := exec.LookPath("garmin-desktop")
	if err != nil {
		self, executableErr := os.Executable()
		if executableErr != nil {
			return executableErr
		}

		app = filepath.Join(filepath.Dir(self), "garmin-desktop")
		if _, err := os.Stat(app); err != nil {
			return errors.New("install the garminctl Linux package first")
		}
	}

	appArgs, err := c.desktopArgs(ctx, nil)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, app, appArgs...) //nolint:gosec // installed application from PATH or next to this executable, no shell.
	cmd.Stdout, cmd.Stderr = c.out, c.errOut

	return cmd.Run()
}

// desktopArgs turns the global -root and -device options into arguments of
// the desktop app. hostPath, when set, converts the absolute root for the host
// that runs the app.
func (c *cli) desktopArgs(ctx context.Context, hostPath func(context.Context, string) (string, error)) ([]string, error) {
	var args []string

	if c.root != "" {
		root, err := filepath.Abs(c.root)
		if err != nil {
			return nil, err
		}

		if hostPath != nil {
			if root, err = hostPath(ctx, root); err != nil {
				return nil, err
			}
		}

		args = append(args, "-root", root)
	}

	if c.devID != "" {
		args = append(args, "-device", c.devID)
	}

	return args, nil
}

// windowsPath converts a WSL path for the Windows app; on Windows it is one already.
func windowsPath(ctx context.Context, path string) (string, error) {
	if runtime.GOOS == "windows" {
		return path, nil
	}

	out, err := exec.CommandContext(ctx, "wslpath", "-w", path).Output() //nolint:gosec // argv is fixed; path is the caller's own -root directory
	if err != nil {
		return "", fmt.Errorf("wslpath: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// desktopLaunchScript starts the installed Windows app with args. Start-Process
// joins its arguments with spaces, so each one is quoted for the Windows command line.
func desktopLaunchScript(args []string) string {
	const script = `$ErrorActionPreference='Stop'; $dir=(Get-ItemProperty 'HKCU:\Software\garminctl' -ErrorAction SilentlyContinue).InstallDir; if (!$dir) { $dir=Join-Path $env:LOCALAPPDATA 'Programs\garminctl' }; $app=Join-Path $dir 'garmin-desktop.exe'; if (!(Test-Path $app)) { throw 'Install garminctl Windows Setup from GitHub Releases first.' }; Start-Process -FilePath $app`

	if len(args) == 0 {
		return script
	}

	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(windowsCommandLineArg(arg), "'", "''") + "'"
	}

	return script + " -ArgumentList @(" + strings.Join(quoted, ", ") + ")"
}

// windowsCommandLineArg quotes one argument the way Windows programs split
// their command line: backslashes count only before a quote.
func windowsCommandLineArg(arg string) string {
	var b strings.Builder

	b.WriteByte('"')

	slashes := 0

	for _, r := range arg {
		switch r {
		case '\\':
			slashes++

			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
		}

		slashes = 0

		b.WriteRune(r)
	}

	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')

	return b.String()
}
