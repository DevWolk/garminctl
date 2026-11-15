package updatetransport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/hostexec"
	"github.com/DevWolk/garminctl/internal/hostfile"
)

var errInstallerOutcomeUnknown = errors.New("installer is still running; its result is unknown; do not retry automatically")

type installerInvocation struct {
	program   string
	args      []string
	setupRoot string
}

func nativeInstallerCommand(r installRequest, directory string) (installerInvocation, error) {
	path := filepath.Join(directory, r.Release.Package.Name)
	switch r.Target.Installation {
	case InstallSetup:
		return installerInvocation{program: path, args: []string{"/S", "/D=" + r.Install.Root}, setupRoot: r.Install.Root}, nil
	case InstallDEB:
		args := []string{"/usr/bin/apt-get", "--yes", "--no-remove"}
		if r.Install.AllowDowngrade {
			args = append(args, "--allow-downgrades")
		}

		return installerInvocation{program: "/usr/bin/pkexec", args: append(args, "install", path)}, nil
	case InstallRPM:
		return installerInvocation{program: "/usr/bin/pkexec", args: []string{"/usr/bin/dnf", "--assumeyes", "install", path}}, nil
	default:
		return installerInvocation{}, errors.New("native installer does not handle portable files")
	}
}

func runNativeInstaller(ctx context.Context, r installRequest, directory string) error {
	invocation, err := nativeInstallerCommand(r, directory)
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

func restartInstalledDesktop(ctx context.Context, options InstallOptions, tag string) error {
	for _, path := range []string{options.CLI, options.Desktop} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return errors.New("installed executable is not a regular file")
		}
	}

	bound, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	command := exec.CommandContext(bound, options.CLI, "version") //nolint:gosec // Observed native CLI path, validated basename and regular file; only the fixed version argument.

	var output stageDiagnostics

	command.Stdout = &output
	command.Stderr = &output
	command.WaitDelay = time.Second
	hostexec.HideConsole(command)

	if err := command.Run(); err != nil {
		return errors.Join(err, bound.Err())
	}

	value, ok := strings.CutPrefix(strings.TrimSpace(output.String()), "garminctl ")
	if !ok || value != tag {
		return errors.New("installed CLI version does not match the selected release; use the retained package to repair")
	}

	return startInstalledDesktop(options)
}

func startInstalledDesktop(options InstallOptions) error {
	command := exec.Command(options.Desktop, options.Args...) //nolint:gosec,noctx // Observed executable and original arguments; the restarted application outlives the completed helper.
	command.Dir = filepath.Dir(options.Desktop)
	hostexec.HideConsole(command)

	if err := command.Start(); err != nil {
		return fmt.Errorf("restart installed desktop: %w", err)
	}

	return command.Process.Release()
}
