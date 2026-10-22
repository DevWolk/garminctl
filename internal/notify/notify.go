// Package notify shows a desktop notification on Windows, also from WSL.
package notify

import (
	"fmt"
	"os/exec"
	"strings"

	"garminctl/internal/device/wpd"
)

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Send shows a balloon notification through PowerShell. It never blocks and
// does nothing where no powershell.exe is reachable.
func Send(title, text string) {
	ps := wpd.PowerShellPath()
	if ps == "" {
		return
	}
	script := fmt.Sprintf("Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $n = New-Object System.Windows.Forms.NotifyIcon; $n.Icon = [System.Drawing.SystemIcons]::Information; $n.Visible = $true; $n.ShowBalloonTip(8000, %s, %s, [System.Windows.Forms.ToolTipIcon]::Info); Start-Sleep -Seconds 9; $n.Dispose()", psQuote(title), psQuote(text))
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}
