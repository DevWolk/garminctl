//go:build !windows

package hostexec

import "os/exec"

// HideConsole has no effect on platforms that do not create Windows consoles.
func HideConsole(_ *exec.Cmd) {}
