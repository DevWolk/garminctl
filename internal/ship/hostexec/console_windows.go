package hostexec

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// HideConsole prevents a background console process from opening a terminal
// window. Redirected stdin/stdout/stderr still work normally.
func HideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
