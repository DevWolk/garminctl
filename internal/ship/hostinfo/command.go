package hostinfo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/hostexec"
)

var errNoPackageOwner = errors.New("no package owns this file")

func runProbe(ctx context.Context, kind probeCommand, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var command *exec.Cmd

	switch kind {
	case probeCLI:
		command = exec.CommandContext(ctx, path, "version")
	case probeDPKG:
		command = exec.CommandContext(ctx, "dpkg-query", "--search", "--", path) //nolint:gosec // absolute local regular-file path is a data argument after --; no shell or request-controlled command.
	case probeRPM:
		command = exec.CommandContext(ctx, "rpm", "-qf", "--qf", "%{NAME}\n", "--", path) //nolint:gosec // absolute local regular-file path is a data argument after --; no shell or request-controlled command.
	default:
		return "", errors.New("unsupported inventory probe")
	}

	command.Env = append(os.Environ(), "LC_ALL=C")
	command.WaitDelay = time.Second
	hostexec.HideConsole(command)

	output, diagnostics := probeOutput{cancel: cancel}, probeOutput{cancel: cancel}
	command.Stdout = &output
	command.Stderr = &diagnostics

	err := command.Run()
	if errors.Is(output.err, io.ErrShortBuffer) || errors.Is(diagnostics.err, io.ErrShortBuffer) {
		return "", io.ErrShortBuffer
	}

	if ctx.Err() != nil {
		return "", errors.Join(err, ctx.Err())
	}

	if err != nil && unownedQuery(kind, path, output.String(), diagnostics.String(), err) {
		return "", errNoPackageOwner
	}

	return output.String(), err
}

func unownedQuery(kind probeCommand, path, out, diagnostics string, err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return false
	}

	if kind == probeDPKG {
		return strings.TrimSpace(diagnostics) == "dpkg-query: no path found matching pattern "+path && strings.TrimSpace(out) == ""
	}

	return kind == probeRPM && strings.TrimSpace(out) == "file "+path+" is not owned by any package" && strings.TrimSpace(diagnostics) == ""
}

type probeOutput struct {
	buffer bytes.Buffer
	cancel context.CancelFunc
	err    error
}

func (p *probeOutput) Write(data []byte) (int, error) {
	if p.buffer.Len()+len(data) > 8<<10 {
		p.err = io.ErrShortBuffer
		p.cancel()

		return 0, p.err
	}

	return p.buffer.Write(data)
}

func (p *probeOutput) String() string { return p.buffer.String() }
