package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DevWolk/garminctl/internal/paths"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// ApplicationStageCommand is a private subprocess protocol, not user install API.
const ApplicationStageCommand = updatetransport.StageCommand

// NewApplicationStager binds a trusted local CLI/version and compiled key to the
// application's private cache, without creating a watch runtime or doing IO.
func NewApplicationStager(cliPath, version, compiledKey string) (*updatetransport.Stager, error) {
	key, err := applicationStageKey(compiledKey)
	if err != nil {
		return nil, err
	}

	return updatetransport.NewStager(updatetransport.StagerConfig{
		CLIPath: cliPath, BuildVersion: version,
		CacheDir: applicationStageCache(), PublicKey: key,
	})
}

// RunApplicationStage is the compiled CLI worker composition. It cannot stage
// beside an installed CLI: only one marked operation under our cache is valid.
func RunApplicationStage(ctx context.Context, version, compiledKey string, input io.Reader, output io.Writer) error {
	key, err := applicationStageKey(compiledKey)
	if err != nil {
		return err
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	directory := filepath.Dir(executable)
	if err := validateApplicationStageLocation(applicationStageCache(), directory); err != nil {
		return err
	}

	return updatetransport.RunStageWorker(ctx, updatetransport.WorkerConfig{
		BuildVersion: version, PublicKey: key, Directory: directory,
	}, input, output)
}

func applicationStageCache() string { return filepath.Join(paths.Cache(), "application-staging") }

func applicationStageKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("application updater compiled public key is not configured or invalid")
	}

	return key, nil
}

func validateApplicationStageLocation(cache, directory string) error {
	relative, err := filepath.Rel(cache, directory)
	if err != nil {
		return err
	}

	if filepath.Dir(relative) != "." || !strings.HasPrefix(relative, "stage-") || relative == "stage-" {
		return errors.New("application worker must run from a private cached CLI operation")
	}

	return nil
}
