package updatetransport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/hostexec"
)

// StagerConfig comes from local composition, never a JSON request.
type StagerConfig struct {
	CLIPath, CacheDir, BuildVersion string
	PublicKey                       []byte
}

// Stager owns no watch or background job; Stage joins one cached CLI child.
type Stager struct{ cliPath, cacheDir, version, key string }

// StagedPackage owns one private operation directory until Close.
// Its paths and copied metadata do not grant installer permission.
type StagedPackage struct {
	directory, path, signature, digest string
	release                            ResolvedRelease
	once                               sync.Once
	closeErr                           error
}

// NewStager validates the locally supplied executable/cache and compiled key.
func NewStager(config StagerConfig) (*Stager, error) {
	key, err := keyID(config.PublicKey)
	if err != nil {
		return nil, err
	}

	if config.CLIPath == "" || config.CacheDir == "" || config.BuildVersion == "" {
		return nil, errors.New("stager requires a CLI path, private cache and build version")
	}

	cli, err := filepath.Abs(config.CLIPath)
	if err != nil {
		return nil, err
	}

	cache, err := filepath.Abs(config.CacheDir)
	if err != nil {
		return nil, err
	}

	return &Stager{cliPath: cli, cacheDir: cache, version: config.BuildVersion, key: key}, nil
}

// Stage copies the trusted CLI, streams bounded IPC and joins before returning.
// Cancellation/failure removes the operation; success transfers it to the result.
func (s *Stager) Stage(ctx context.Context, target MatchRequest, release ResolvedRelease, progress func(StageProgress)) (*StagedPackage, error) {
	return s.run(ctx, target, release, false, nil, progress)
}

func (s *Stager) run(ctx context.Context, target MatchRequest, release ResolvedRelease, offline bool, prepare func(context.Context, string) error, progress func(StageProgress)) (result *StagedPackage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if s == nil || s.cliPath == "" {
		return nil, errors.New("application stager is not configured")
	}

	if err := validateResolved(target, release); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	directory, cli, err := prepareStage(ctx, s.cacheDir, s.cliPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if result == nil {
			err = errors.Join(err, removeOperation(directory))
		}
	}()

	operation, err := prepareStageOperation(ctx, directory, prepare)
	if err != nil {
		return nil, err
	}
	// Display fields cannot inflate the worker protocol; they remain in the result.
	wireRelease := release
	wireRelease.Release.Name = ""
	wireRelease.Release.Notes = ""
	request := stageRequest{Protocol: stageProtocol, Operation: operation, BuildVersion: s.version, KeyID: s.key, Target: target, Release: wireRelease}

	input, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	if len(input) > maxStageRequest {
		return nil, errors.New("staging request exceeds the byte limit")
	}

	output := &stageOutput{operation: operation, version: s.version, key: s.key, size: release.Package.Size, progress: progress, cancel: cancel}
	diagnostics := &stageDiagnostics{}

	runErr := runStageProcess(ctx, func() *exec.Cmd {
		cmd := cachedCLICommand(ctx, cli, offline)
		cmd.Dir = directory
		cmd.Stdin = bytes.NewReader(input)
		cmd.Stdout = output
		cmd.Stderr = diagnostics
		cmd.WaitDelay = 3 * time.Second
		hostexec.HideConsole(cmd)

		return cmd
	})
	if err := errors.Join(explainHelperStart(runErr), output.err, ctx.Err()); err != nil {
		return nil, fmt.Errorf("application staging failed (%s): %w", diagnostics.String(), err)
	}

	if err := output.finish(); err != nil {
		return nil, err
	}

	if err := verifyStagedFiles(ctx, directory, release, output.digest); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &StagedPackage{directory: directory, path: filepath.Join(directory, release.Package.Name), signature: filepath.Join(directory, release.Signature.Name), digest: output.digest, release: release}, nil
}

func runStageProcess(ctx context.Context, create func() *exec.Cmd) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		cmd := create()
		err := cmd.Run()
		// A failed exec can transiently see an inherited writable descriptor.
		// Only ETXTBSY before a target process exists may be retried.
		if err == nil || cmd.Process != nil || !errors.Is(err, syscall.ETXTBSY) || attempt == 4 {
			return err
		}

		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func newOperationID() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(value[:]), nil
}

// Path returns the retained original package, including archives before extraction.
func (p *StagedPackage) Path() string { return p.path }

// SignaturePath returns the retained raw sibling signature.
func (p *StagedPackage) SignaturePath() string { return p.signature }

// Digest returns SHA256 of the exact verified stream.
func (p *StagedPackage) Digest() string { return p.digest }

// Release returns a copy of the selected metadata.
func (p *StagedPackage) Release() ResolvedRelease { return p.release }

// Close removes only this completed operation, after its child has been joined.
func (p *StagedPackage) Close() error {
	p.once.Do(func() { p.closeErr = removeOperation(p.directory) })
	return p.closeErr
}

func cachedCLICommand(ctx context.Context, cli string, offline bool) *exec.Cmd {
	if offline {
		return exec.CommandContext(ctx, cli, VerifyCommand)
	}

	return exec.CommandContext(ctx, cli, StageCommand)
}

func prepareStageOperation(ctx context.Context, directory string, prepare func(context.Context, string) error) (string, error) {
	operation, err := newOperationID()
	if err != nil {
		return "", err
	}

	if err := writeOperationMarker(directory, operation); err != nil {
		return "", err
	}

	if prepare != nil {
		if err := prepare(ctx, directory); err != nil {
			return "", err
		}
	}

	return operation, nil
}
