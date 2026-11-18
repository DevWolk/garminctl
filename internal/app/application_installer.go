package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/paths"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func applicationInstallCache() string { return filepath.Join(paths.Cache(), "application-installing") }

// RunApplicationInstall cannot run from an installed or ordinary staging CLI.
func RunApplicationInstall(ctx context.Context, version, key string, input io.Reader, output io.Writer) error {
	return runApplicationInstall(ctx, version, key, input, output, nil)
}

// runApplicationInstall takes the helper's host readiness check; nil selects the platform's own.
func runApplicationInstall(ctx context.Context, version, key string, input io.Reader, output io.Writer, hostCheck func(updatetransport.InstallKind, string, string) error) error {
	return runApplicationWorkerAt(ctx, version, key, input, output, applicationInstallCache(), func(ctx context.Context, config updatetransport.WorkerConfig, input io.Reader, output io.Writer) error {
		stream, ok := input.(io.ReadCloser)
		if !ok {
			return errors.New("installer worker requires its owned input pipe")
		}

		return updatetransport.RunInstallWorker(ctx, updatetransport.InstallWorkerConfig{Directory: config.Directory, BuildVersion: config.BuildVersion, PublicKey: config.PublicKey, ProbeMCP: mcpx.ProbeServer, HostCheck: hostCheck}, stream, output)
	})
}

// RunApplicationPackage is started as root by the system authorization prompt,
// from the installed CLI. It trusts only the compiled key: the request names a
// directory that its unprivileged sender can still change.
func RunApplicationPackage(ctx context.Context, version, key string, input io.Reader, output io.Writer) error {
	compiled, err := applicationStageKey(key)
	if err != nil {
		return err
	}

	return updatetransport.RunPackageWorker(ctx, updatetransport.PackageWorkerConfig{BuildVersion: version, PublicKey: compiled}, input, output)
}

// applicationInstallPrepare starts the private OS helper for one verified package.
type applicationInstallPrepare func(context.Context, updatetransport.StagerConfig, updatetransport.MatchRequest, *updatetransport.StagedPackage, updatetransport.InstallOptions) (application.PendingInstallation, error)

func prepareApplicationInstallHelper(ctx context.Context, config updatetransport.StagerConfig, target updatetransport.MatchRequest, payload *updatetransport.StagedPackage, options updatetransport.InstallOptions) (application.PendingInstallation, error) {
	installer, err := updatetransport.NewInstaller(config)
	if err != nil {
		return nil, err
	}

	pending, err := installer.Prepare(ctx, target, payload, options)
	if err != nil {
		return nil, err
	}

	return pending, nil
}

// LastApplicationInstallation reads the newest helper record under the
// installer cache; nil means no committed installation left one. It starts no
// installer and needs no backend or compiled key.
func LastApplicationInstallation(ctx context.Context) (*application.InstallationOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result, err := updatetransport.LastInstallResult(applicationInstallCache())
	if err != nil || result == nil {
		return nil, err
	}

	return &application.InstallationOutcome{Tag: result.Tag, Status: result.Status, Error: result.Error, From: result.From, Finished: result.Finished, Directory: result.Directory}, nil
}

// LastInstallation lets the native window show an installation that failed after the application closed.
func (r *applicationNativeReader) LastInstallation(ctx context.Context) (*application.InstallationOutcome, error) {
	return LastApplicationInstallation(ctx)
}

type applicationInstallerPort struct {
	key  string
	args []string
	// prepare is the OS helper; nil selects the production helper.
	prepare applicationInstallPrepare
}

func (p applicationInstallerPort) Prepare(ctx context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
	key, err := applicationStageKey(p.key)
	if err != nil {
		return nil, err
	}

	packageCopy, ok := input.Update.(*verifiedApplicationPackage)
	if !ok || packageCopy.staged == nil || packageCopy.Record() != input.Info.Update {
		return nil, errors.New("installer requires this composition's verified package")
	}

	prepare := p.prepare
	if prepare == nil {
		prepare = prepareApplicationInstallHelper
	}

	value := input.Info.Update
	config := updatetransport.StagerConfig{CLIPath: input.Info.Inventory.CLI.Path, BuildVersion: input.Info.Inventory.CLI.Version, PublicKey: key, CacheDir: applicationInstallCache()}
	target := updatetransport.MatchRequest{ReleaseTag: value.Tag, OS: value.OS, Arch: value.Arch, Installation: updatetransport.InstallKind(value.Installation)}

	return prepare(ctx, config, target, packageCopy.staged, applicationInstallOptions(input, p.args))
}

func applicationInstallOptions(input application.InstallationInput, args []string) updatetransport.InstallOptions {
	// Relative restart arguments refer to this process's working directory.
	// Without one the helper falls back to the installation directory.
	directory, err := os.Getwd()
	if err != nil {
		directory = ""
	}

	return updatetransport.InstallOptions{
		Root:           input.Info.Inventory.Installation.Root,
		Desktop:        input.Info.Inventory.Desktop.Path,
		CLI:            input.Info.Inventory.CLI.Path,
		Args:           args,
		Dir:            directory,
		AllowDowngrade: input.Rollback && input.Info.Update.Installation == string(updatetransport.InstallDEB),
	}
}

func (b *ApplicationBackend) stopFor(address string, owned bool) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if !owned {
			return errors.New("external backend must be stopped by its owner")
		}

		runtime, err := b.current(address)
		if err != nil {
			return err
		}

		if runtime.Manager().Gate().Snapshot().State != lifecycle.Maintenance {
			return errors.New("installer shutdown requires owned maintenance")
		}

		b.mu.Lock()
		done := b.done
		b.mu.Unlock()

		bound, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		runtime.Stop()

		select {
		case <-bound.Done():
			return bound.Err()
		case <-done:
			b.mu.Lock()
			err := b.runErr
			b.mu.Unlock()

			return err
		}
	}
}

// Handoff is internal to the owned native installation pipeline.
func (r *applicationNativeReader) Handoff(ctx context.Context, prepared *application.PreparedUpdate, consent application.InstallConsent) (*application.InstallHandoffResult, error) {
	if r.handoff == nil {
		return nil, abortApplicationSnapshot(prepared, errors.New("native installer handoff is not configured"))
	}

	return r.handoff.Run(ctx, prepared, consent)
}
