package updatetransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DevWolk/garminctl/internal/hostexec"
)

func startInstalledDesktop(ctx context.Context, options InstallOptions, tag, directory string, probe func(context.Context, string, string) error) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()

	ticket, err := newStartupTicket(root, directory)
	if err != nil {
		return err
	}

	encoded, err := json.Marshal(ticket)
	if err != nil {
		return err
	}

	command := exec.Command(options.Desktop, options.Args...) //nolint:gosec,noctx // Observed native path/arguments; the restarted app must survive the bounded monitor and is never killed for a readiness failure.
	command.Dir = filepath.Dir(options.Desktop)

	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, StartupEnvironment+"=") {
			command.Env = append(command.Env, entry)
		}
	}

	command.Env = append(command.Env, StartupEnvironment+"="+string(encoded))
	hostexec.HideConsole(command)

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := command.Start(); err != nil {
		return err
	}

	if err := command.Process.Release(); err != nil {
		return err
	}

	report, err := awaitStartup(ctx, root, ticket, tag)
	if err != nil {
		return err
	}

	return checkStartupServices(ctx, report, tag, probe)
}

func newStartupTicket(root *os.Root, directory string) (startupTicket, error) {
	var ticket startupTicket
	if _, err := root.Lstat(startupReceiptName); !errors.Is(err, os.ErrNotExist) {
		return ticket, errors.New("startup report already exists or cannot be checked")
	}

	file, err := root.Open(operationMarker)
	if err != nil {
		return ticket, err
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, 65))
	if err != nil || !validPackageID(string(data)) {
		return ticket, errors.New("invalid startup operation marker")
	}

	token, err := newOperationID()
	if err != nil {
		return ticket, err
	}

	ticket = startupTicket{Protocol: 1, Directory: filepath.Base(directory), Operation: string(data), Token: token}

	return ticket, validateStartupTicket(ticket)
}
