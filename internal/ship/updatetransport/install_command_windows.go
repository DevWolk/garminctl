package updatetransport

import (
	"os/exec"

	"github.com/DevWolk/garminctl/internal/hostexec"
)

func configureInstallerCommand(command *exec.Cmd, invocation installerInvocation) {
	hostexec.HideConsole(command)

	if invocation.setupRoot != "" {
		// NSIS requires unquoted /D as the last argument, including paths with spaces.
		command.SysProcAttr.CmdLine = "\"" + command.Path + "\" /S /D=" + invocation.setupRoot
	}
}
