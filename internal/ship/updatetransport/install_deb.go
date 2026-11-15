package updatetransport

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	debMetadataTool = "/usr/bin/dpkg-deb"
	debQueryTool    = "/usr/bin/dpkg-query"
	debCompareTool  = "/usr/bin/dpkg"
)

type nativeVersionQuery func(context.Context, string, ...string) (string, error)

func prepareNativeInstaller(ctx context.Context, r installRequest, directory string, query nativeVersionQuery) (installerInvocation, error) {
	if err := ctx.Err(); err != nil {
		return installerInvocation{}, err
	}

	if r.Target.Installation == InstallDEB && !r.Install.AllowDowngrade {
		// Release selection and consent already belong to Application. Native
		// ordering can reverse that selection, e.g. 1.2.3.rc.1 -> 1.2.3.
		lower, err := debTargetIsLower(ctx, filepath.Join(directory, r.Release.Package.Name), query)
		if err != nil {
			return installerInvocation{}, fmt.Errorf("compare selected DEB with installed package: %w", err)
		}

		r.Install.AllowDowngrade = lower
	}

	return nativeInstallerCommand(r, directory)
}

func debTargetIsLower(ctx context.Context, path string, query nativeVersionQuery) (bool, error) {
	target, err := query(ctx, debMetadataTool, "--field", path, "Version")
	if err != nil {
		return false, err
	}

	current, err := query(ctx, debQueryTool, "-W", "-f=${Version}", "garminctl")
	if err != nil {
		return false, err
	}

	target, current = strings.TrimSpace(target), strings.TrimSpace(current)
	if !validNativeVersion(target) || !validNativeVersion(current) {
		return false, errors.New("native package version is missing or invalid")
	}

	_, err = query(ctx, debCompareTool, "--compare-versions", target, "lt", current)
	if err == nil {
		return true, nil
	}

	var exit interface{ ExitCode() int }
	if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}

	return false, err
}

func validNativeVersion(value string) bool {
	return len(value) > 0 && len(value) <= 256 && value[0] >= '0' && value[0] <= '9' &&
		!strings.ContainsAny(value, " \t\r\n\x00")
}

func queryNativeVersion(ctx context.Context, program string, args ...string) (string, error) {
	bound, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	command := exec.CommandContext(bound, program, args...) //nolint:gosec // Private caller supplies fixed read-only dpkg tools and separate argv, never a shell or a caller-supplied executable.
	command.WaitDelay = time.Second

	var output, diagnostic stageDiagnostics

	command.Stdout = &output

	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("native version query: %s: %w", diagnostic.String(), errors.Join(err, bound.Err()))
	}

	if len(output.data) > 256 {
		return "", errors.New("native version query exceeds the byte limit")
	}

	return output.String(), nil
}
