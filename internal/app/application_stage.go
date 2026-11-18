package app

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/paths"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// ApplicationStageCommand is a private subprocess protocol, not user install API.
const ApplicationStageCommand = updatetransport.StageCommand

// ApplicationVerifyCommand is the private offline-verification subprocess.
const ApplicationVerifyCommand = updatetransport.VerifyCommand

// ApplicationInstallCommand is the native-confirmed cached executor entry.
const ApplicationInstallCommand = updatetransport.InstallCommand

// ApplicationPackageCommand is the installed CLI's privileged DEB/RPM step.
const ApplicationPackageCommand = updatetransport.PackageCommand

// NewApplicationPackageCache retains packages separately from stage operations.
// Both factories require the same compiled key and create no watch runtime.
func NewApplicationPackageCache(cliPath, version, compiledKey string) (*updatetransport.PackageCache, error) {
	stager, err := NewApplicationStager(cliPath, version, compiledKey)
	if err != nil {
		return nil, err
	}

	return updatetransport.NewPackageCache(filepath.Join(paths.Cache(), "application-packages"), stager)
}

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
	return runApplicationWorker(ctx, version, compiledKey, input, output, updatetransport.RunStageWorker)
}

// RunApplicationVerify authenticates retained bytes without using the network.
func RunApplicationVerify(ctx context.Context, version, compiledKey string, input io.Reader, output io.Writer) error {
	return runApplicationWorker(ctx, version, compiledKey, input, output, updatetransport.RunVerifyWorker)
}

func runApplicationWorker(ctx context.Context, version, compiledKey string, input io.Reader, output io.Writer, worker func(context.Context, updatetransport.WorkerConfig, io.Reader, io.Writer) error) error {
	return runApplicationWorkerAt(ctx, version, compiledKey, input, output, applicationStageCache(), worker)
}

func runApplicationWorkerAt(ctx context.Context, version, compiledKey string, input io.Reader, output io.Writer, cache string, worker func(context.Context, updatetransport.WorkerConfig, io.Reader, io.Writer) error) error {
	key, err := applicationStageKey(compiledKey)
	if err != nil {
		return err
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	directory := filepath.Dir(executable)
	if err := validateApplicationStageLocation(cache, directory); err != nil {
		return err
	}

	return worker(ctx, updatetransport.WorkerConfig{
		BuildVersion: version, PublicKey: key, Directory: directory,
	}, input, output)
}

func applicationStageCache() string { return filepath.Join(paths.Cache(), "application-staging") }

func applicationStageKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || updatetransport.ValidatePublicKey(key) != nil {
		return nil, errors.New("application updater compiled public key is not configured or invalid")
	}

	return key, nil
}

func validateApplicationStageLocation(cache, directory string) error {
	refusal := errors.New("application worker must run from a private cached CLI operation")

	// The executable's directory is reported resolved, while the cache may be
	// named through a linked home or cache directory; compare the real paths.
	cache, err := filepath.EvalSymlinks(cache)
	if err != nil {
		return refusal
	}

	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return refusal
	}

	relative, err := filepath.Rel(cache, directory)
	if err != nil {
		return err
	}

	if filepath.Dir(relative) != "." || !strings.HasPrefix(relative, "stage-") || relative == "stage-" {
		return refusal
	}

	return nil
}
