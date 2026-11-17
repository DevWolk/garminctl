package updatetransport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// preflightNativeInstall checks what the installer needs while the application
// still runs. Whether an authorization agent is registered is not visible
// without a session-bus client, so a missing agent is still reported only by
// the recorded installation result.
func preflightNativeInstall(r installRequest, directory string) error {
	if err := checkPrivateAncestry(directory); err != nil {
		return err
	}

	tools := []string{privilegeTool, rpmInstaller, rpmMetadataTool}
	if r.Target.Installation == InstallDEB {
		tools = []string{privilegeTool, debInstaller, debMetadataTool, debQueryTool, debCompareTool}
	}

	if err := requireInstallTools(tools...); err != nil {
		return err
	}

	return checkSystemProgram(r.Install.CLI)
}

func requireInstallTools(paths ...string) error {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("updating needs %s, which is not available on this system; install it or update with the system package manager", path)
		}
	}

	return nil
}

// checkSystemProgram accepts a program only where nobody but root can replace
// it, because the authorization prompt runs it as root.
func checkSystemProgram(path string) error {
	refusal := fmt.Errorf("the installed command-line tool %s is not a system-owned file and cannot perform the privileged installation; update with the system package manager", path)

	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return refusal
	}

	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return errors.Join(refusal, err)
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return refusal
		}

		if leaf := current == path; (leaf && !info.Mode().IsRegular()) || (!leaf && !info.IsDir()) {
			return refusal
		}

		if current == filepath.Dir(current) {
			return nil
		}
	}
}
