// Package notify shows a desktop notification on Windows, also from WSL.
package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/device/wpd"
	"github.com/DevWolk/garminctl/internal/ship/hostexec"
)

// psQuotes doubles every character PowerShell treats as a single quote:
// ASCII ' and U+2018, U+2019, U+201A, U+201B.
var psQuotes = strings.NewReplacer("'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019", "\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b")

func psQuote(s string) string { return "'" + psQuotes.Replace(s) + "'" }

// Send shows a balloon notification through PowerShell and waits for its child.
// The caller owns its worker. Ending ctx removes the balloon and joins the child;
// where PowerShell is unavailable, Send returns immediately.
func Send(ctx context.Context, title, text string) {
	ps := wpd.PowerShellPath()
	if ps == "" {
		return
	}

	script := fmt.Sprintf("Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; "+
		"$n = New-Object System.Windows.Forms.NotifyIcon; $n.Icon = [System.Drawing.SystemIcons]::Information; $n.Visible = $true; "+
		"$n.ShowBalloonTip(8000, %s, %s, [System.Windows.Forms.ToolTipIcon]::Info); Start-Sleep -Seconds 9; $n.Dispose()",
		psQuote(title), psQuote(text))

	//nolint:gosec // argv is fixed: ps is powershell.exe as found by PowerShellPath; title and text are quoted
	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	hostexec.HideConsole(cmd)

	_ = cmd.Run() // notification failure does not fail an otherwise completed sync
}
