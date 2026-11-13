package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/paths"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func applicationInstallCache() string { return filepath.Join(paths.Cache(), "application-installing") }

// RunApplicationInstall cannot run from an installed or ordinary staging CLI.
func RunApplicationInstall(ctx context.Context, version, key string, input io.Reader, output io.Writer) error {
	return runApplicationWorkerAt(ctx, version, key, input, output, applicationInstallCache(), func(ctx context.Context, config updatetransport.WorkerConfig, input io.Reader, output io.Writer) error {
		stream, ok := input.(io.ReadCloser)
		if !ok {
			return errors.New("installer worker requires its owned input pipe")
		}

		return updatetransport.RunInstallWorker(ctx, updatetransport.InstallWorkerConfig{Directory: config.Directory, BuildVersion: config.BuildVersion, PublicKey: config.PublicKey}, stream, output)
	})
}

type applicationInstallerPort struct {
	key  string
	args []string
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

	installer, err := updatetransport.NewInstaller(updatetransport.StagerConfig{CLIPath: input.Info.Inventory.CLI.Path, BuildVersion: input.Info.Inventory.CLI.Version, PublicKey: key, CacheDir: applicationInstallCache()})
	if err != nil {
		return nil, err
	}

	value := input.Info.Update

	pending, err := installer.Prepare(ctx, updatetransport.MatchRequest{ReleaseTag: value.Tag, OS: value.OS, Arch: value.Arch, Installation: updatetransport.InstallKind(value.Installation)}, packageCopy.staged, updatetransport.InstallOptions{Root: input.Info.Inventory.Installation.Root, Desktop: input.Info.Inventory.Desktop.Path, CLI: input.Info.Inventory.CLI.Path, Args: p.args})
	if err != nil {
		return nil, err
	}

	return pending, nil
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
