package updatetransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/hostexec"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
)

var errInstallerOutcomeUnknown = errors.New("installer is still running; its result is unknown; do not retry automatically")

const (
	privilegeTool = "/usr/bin/pkexec"
	debInstaller  = "/usr/bin/apt-get"
	rpmInstaller  = "/usr/bin/dnf"
)

type installerInvocation struct {
	program   string
	args      []string
	setupRoot string
	// input is the request of the privileged package step; empty for Setup.
	input []byte
}

// nativeInstallerCommand names the OS installer itself. For DEB/RPM that is the
// package manager the privileged step runs on its own copy of the package.
func nativeInstallerCommand(r installRequest, directory string) (installerInvocation, error) {
	path := filepath.Join(directory, r.Release.Package.Name)
	switch r.Target.Installation {
	case InstallSetup:
		return installerInvocation{program: path, args: []string{"/S", "/D=" + r.Install.Root}, setupRoot: r.Install.Root}, nil
	case InstallDEB:
		args := []string{"--yes", "--no-remove"}
		if r.Install.AllowDowngrade {
			args = append(args, "--allow-downgrades")
		}

		return installerInvocation{program: debInstaller, args: append(args, "install", path)}, nil
	case InstallRPM:
		return installerInvocation{program: rpmInstaller, args: []string{"--assumeyes", "install", path}}, nil
	default:
		return installerInvocation{}, errors.New("native installer does not handle portable files")
	}
}

// handoffInvocation is what the unprivileged helper starts. A package in the
// operation directory stays replaceable by its owner until root reads it, so
// DEB/RPM go to the installed CLI's privileged step, which copies and
// authenticates the bytes before the package manager sees them.
func handoffInvocation(r installRequest, directory string) (installerInvocation, error) {
	if r.Target.Installation != InstallDEB && r.Target.Installation != InstallRPM {
		return nativeInstallerCommand(r, directory)
	}

	input, err := json.Marshal(packageRequest{Protocol: packageProtocol, BuildVersion: r.BuildVersion, KeyID: r.KeyID, Target: r.Target, Directory: directory, Size: r.Release.Package.Size, Digest: r.Digest, AllowDowngrade: r.Install.AllowDowngrade})
	if err != nil {
		return installerInvocation{}, err
	}

	return installerInvocation{program: privilegeTool, args: []string{r.Install.CLI, PackageCommand}, input: input}, nil
}

func runNativeInstaller(ctx context.Context, r installRequest, directory string) error {
	invocation, err := handoffInvocation(r, directory)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}

	defer root.Close()

	log, err := root.OpenFile("installer.log", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	if err := hostfile.Protect(filepath.Join(directory, "installer.log"), false); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	// Once started, the OS transaction must not be killed by UI cancellation.
	command := exec.Command(invocation.program, invocation.args...) //nolint:gosec,noctx // Fixed native tool/verified Setup, no shell. An already committed OS transaction must outlive UI/helper timeout rather than be killed mid-replacement.
	command.Dir = directory
	command.Stdout = log
	command.Stderr = log

	if invocation.input != nil {
		command.Stdin = bytes.NewReader(invocation.input)
	}

	configureInstallerCommand(command, invocation)

	if err := command.Start(); err != nil {
		return err
	}

	return waitNativeInstaller(ctx, command)
}

func waitNativeInstaller(ctx context.Context, command *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return errors.Join(errInstallerOutcomeUnknown, ctx.Err())
	}
}

// installedVersionWait bounds the first start of a program the installer has
// just written. Windows scans a new executable before it runs, which can take
// well over the fraction of a second the command itself needs.
const installedVersionWait = 30 * time.Second

// installedVersion asks the installed CLI for its version.
func installedVersion(ctx context.Context, cli string) (string, error) {
	bound, cancel := context.WithTimeout(ctx, installedVersionWait)
	defer cancel()

	command := exec.CommandContext(bound, cli, "version")

	var output stageDiagnostics

	command.Stdout = &output
	command.Stderr = &output
	command.WaitDelay = time.Second
	hostexec.HideConsole(command)

	if err := command.Run(); err != nil {
		return "", errors.Join(err, bound.Err())
	}

	value, ok := strings.CutPrefix(strings.TrimSpace(output.String()), "garminctl ")
	if !ok {
		return "", errors.New("unexpected version output")
	}

	return value, nil
}

func restartInstalledDesktop(ctx context.Context, options InstallOptions, tag, directory string, probe func(context.Context, string, string) error) error {
	for _, path := range []string{options.CLI, options.Desktop} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return errors.New("installed executable is not a regular file")
		}
	}

	value, err := installedVersion(ctx, options.CLI)
	if err != nil {
		return fmt.Errorf("the installed CLI did not report its version: %w", err)
	}

	if value != tag {
		return errors.New("installed CLI version does not match the selected release; use the retained package to repair")
	}

	if err := startInstalledDesktop(ctx, options, tag, directory, probe); err != nil {
		return fmt.Errorf("startup validation failed; use the retained package to repair: %w", err)
	}

	return nil
}
