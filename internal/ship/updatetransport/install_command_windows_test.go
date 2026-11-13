package updatetransport

import (
	"os/exec"
	"testing"
)

func TestSetupDirectoryIsLastAndUnquotedWithSpaces(t *testing.T) {
	t.Parallel()

	const (
		program   = `C:\private cache\package.exe`
		directory = `C:\Apps\Garmin tools`
	)

	command := exec.CommandContext(t.Context(), program, "/S", "/D="+directory)
	configureInstallerCommand(command, installerInvocation{setupRoot: directory})

	if command.SysProcAttr == nil || command.SysProcAttr.CmdLine != `"`+program+`" /S /D=`+directory {
		t.Fatalf("NSIS command line=%+v", command.SysProcAttr)
	}
}
