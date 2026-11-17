package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/device/wpd"
	"github.com/DevWolk/garminctl/internal/hostexec"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

// desktop opens the installed native app; WSL launches the Windows installation.
func (c *cli) desktop(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return clix.UsageErrorf("usage: garminctl desktop")
	}

	if ps := wpd.PowerShellPath(); ps != "" {
		const script = `$ErrorActionPreference='Stop'; $dir=(Get-ItemProperty 'HKCU:\Software\garminctl' -ErrorAction SilentlyContinue).InstallDir; if (!$dir) { $dir=Join-Path $env:LOCALAPPDATA 'Programs\garminctl' }; $app=Join-Path $dir 'garmin-desktop.exe'; if (!(Test-Path $app)) { throw 'Install garminctl Windows Setup from GitHub Releases first.' }; Start-Process -FilePath $app`

		cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", script) //nolint:gosec // fixed script, PowerShell found by the existing WPD platform helper.
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

	cmd := exec.CommandContext(ctx, app) //nolint:gosec // installed application from PATH or next to this executable, no shell.
	cmd.Stdout, cmd.Stderr = c.out, c.errOut

	return cmd.Run()
}
