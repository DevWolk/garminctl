//go:build !windows

package updatetransport

import "os/exec"

func configureInstallerCommand(*exec.Cmd, installerInvocation) {}
