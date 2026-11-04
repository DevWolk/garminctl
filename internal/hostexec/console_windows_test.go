package hostexec

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestHideConsoleKeepsPipes(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	const script = `
Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class ConsoleProbe { [DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow(); }'
$line = [Console]::In.ReadLine()
[Console]::Out.WriteLine(('{0}|{1}' -f [ConsoleProbe]::GetConsoleWindow().ToInt64(), $line))
`

	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	HideConsole(cmd)

	cmd.Stdin = strings.NewReader("bridge-test\n")

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hidden PowerShell: %v; context: %v; stderr: %s", err, ctx.Err(), stderr.String())
	}

	if got := strings.TrimSpace(string(out)); got != "0|bridge-test" {
		t.Fatalf("console handle and pipe response = %q, want 0|bridge-test", got)
	}
}
