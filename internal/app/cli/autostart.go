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

	"github.com/DevWolk/garminctl/internal/device/wpd"
	"github.com/DevWolk/garminctl/internal/hostexec"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

// autostart manages a Startup shortcut for the hidden Windows server.
// Its binary copy in %LOCALAPPDATA%\garminctl does not depend on this checkout.
func (c *cli) autostart(ctx context.Context, args []string) error {
	ps := wpd.PowerShellPath()
	if ps == "" {
		return errors.New("autostart needs Windows (or WSL with powershell.exe reachable)")
	}

	mode := "status"
	if len(args) > 0 {
		mode = args[0]
	}

	const lnk = `$lnk = Join-Path ([Environment]::GetFolderPath('Startup')) 'garminctl.lnk'; `

	var script string

	switch mode {
	case "status":
		script = lnk + `if (Test-Path $lnk) { 'on: ' + $lnk } else { 'off' }`
	case "off":
		script = lnk + `if (Test-Path $lnk) { Remove-Item $lnk; 'removed ' + $lnk } else { 'was off' }`
	case "on":
		exe, err := os.Executable()
		if err != nil {
			return err
		}

		src := exe
		if runtime.GOOS != "windows" { // WSL: use the Windows build next to this binary
			src = filepath.Join(filepath.Dir(exe), "garminctl.exe")
			if _, err := os.Stat(src); err != nil {
				return fmt.Errorf("no Windows build at %s (go build with GOOS=windows first)", src)
			}

			out, err := exec.CommandContext(ctx, "wslpath", "-w", src).Output() //nolint:gosec // argv is fixed; src is our own binary's path
			if err != nil {
				return fmt.Errorf("wslpath: %w", err)
			}

			src = strings.TrimSpace(string(out))
		}

		quoted := "'" + strings.ReplaceAll(src, "'", "''") + "'"
		script = `$dir = Join-Path $env:LOCALAPPDATA 'garminctl'; New-Item -ItemType Directory -Force $dir | Out-Null; ` +
			`Copy-Item -Force ` + quoted + ` (Join-Path $dir 'garminctl.exe'); ` + lnk +
			`$sh = New-Object -ComObject WScript.Shell; $s = $sh.CreateShortcut($lnk); $s.TargetPath = 'powershell.exe'; ` +
			`$s.Arguments = '-NoProfile -WindowStyle Hidden -Command "& ' + [char]39 + (Join-Path $dir 'garminctl.exe') + [char]39 + ' serve"'; ` +
			`$s.WorkingDirectory = $dir; $s.Description = 'garminctl: sync the Garmin watch in the background'; $s.Save(); ` +
			`'on: ' + $lnk + ' (web UI at http://127.0.0.1:8787 after the next sign-in)'`
	default:
		return clix.UsageErrorf("usage: autostart on|off|status")
	}

	//nolint:gosec // argv is fixed: ps is powershell.exe as found by PowerShellPath; the script is built from literals and a quoted path
	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", script)
	hostexec.HideConsole(cmd)
	out, err := cmd.CombinedOutput()
	c.out.Print(strings.TrimRight(string(out), "\r\n") + "\n")

	return err
}
