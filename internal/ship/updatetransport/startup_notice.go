package updatetransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

// StartupEnvironment carries a one-use readiness ticket, never installer authority.
const StartupEnvironment = "GARMINCTL_APPLICATION_STARTUP"

const startupReceiptName = "startup-ready.json"

type startupTicket struct {
	Protocol  int    `json:"protocol"`
	Directory string `json:"directory"`
	Operation string `json:"operation"`
	Token     string `json:"token"`
}

type startupReceipt struct {
	Protocol       int    `json:"protocol"`
	Operation      string `json:"operation"`
	Token          string `json:"token"`
	DesktopVersion string `json:"desktopVersion"`
	BackendURL     string `json:"backendURL"`
	BackendOwned   bool   `json:"backendOwned"`
}

// NewStartupNotice registers a private report without IO; an empty ticket disables it.
// The caller removes the ticket from its environment before starting other children.
func NewStartupNotice(cache, encoded, version string) (func(context.Context, string, bool) error, error) {
	if encoded == "" {
		return nil, nil
	}

	if !filepath.IsAbs(cache) {
		return nil, errors.New("startup report requires an absolute private cache")
	}

	var ticket startupTicket
	if len(encoded) > 2048 || decodeStageJSON([]byte(encoded), &ticket) != nil {
		return nil, errors.New("invalid application startup ticket")
	}

	if err := validateStartupTicket(ticket); err != nil {
		return nil, err
	}

	return func(ctx context.Context, address string, owned bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		directory := filepath.Join(cache, ticket.Directory)

		cacheRoot, err := os.OpenRoot(cache)
		if err != nil {
			return err
		}
		defer cacheRoot.Close()

		root, err := cacheRoot.OpenRoot(ticket.Directory)
		if err != nil {
			return err
		}
		defer root.Close()

		if err := checkOperationMarker(root, ticket.Operation); err != nil {
			return err
		}

		report := startupReceipt{Protocol: 1, Operation: ticket.Operation, Token: ticket.Token, DesktopVersion: version, BackendURL: address, BackendOwned: owned}

		return writeStartupReceipt(ctx, root, directory, report)
	}, nil
}

func validateStartupTicket(ticket startupTicket) error {
	if ticket.Protocol != 1 || !validPackageID(ticket.Operation) || !validPackageID(ticket.Token) {
		return errors.New("invalid application startup ticket identity")
	}

	name := ticket.Directory
	if len(name) > 256 || !strings.HasPrefix(name, "stage-") || name == "stage-" || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return errors.New("startup report requires its private operation directory")
	}

	return nil
}

func writeStartupReceipt(ctx context.Context, root *os.Root, directory string, report startupReceipt) error {
	data, err := json.Marshal(report)
	if err != nil || len(data) > 4096 {
		return errors.New("startup report exceeds its byte limit")
	}

	const pending = ".startup-ready.tmp"

	file, err := root.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close(); _ = root.Remove(pending) }()

	if err := hostfile.Protect(filepath.Join(directory, pending), false); err != nil {
		return err
	}

	if _, err := file.Write(data); err != nil {
		return err
	}

	if err := file.Sync(); err != nil {
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	return root.Rename(pending, startupReceiptName)
}

func readStartupReceipt(root *os.Root) (startupReceipt, error) {
	var result startupReceipt

	file, err := root.Open(startupReceiptName)
	if err != nil {
		return result, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return result, errors.New("invalid application startup report file")
	}

	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return result, err
	}

	if len(data) > 4096 {
		return result, errors.New("startup report exceeds its byte limit")
	}

	err = decodeStageJSON(data, &result)

	return result, err
}
