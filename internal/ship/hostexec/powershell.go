package hostexec

import (
	"os"
	"os/exec"
	"runtime"
)

// PowerShellPath returns the path of powershell.exe, or "" when unavailable.
func PowerShellPath() string {
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			return p
		}

		return ""
	}

	candidates := []string{
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"/mnt/c/Windows/Sysnative/WindowsPowerShell/v1.0/powershell.exe",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}
