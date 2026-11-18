package updatetransport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/hostexec"
	"github.com/DevWolk/garminctl/internal/hostfile"
)

// Installer prepares a separate cached CLI executor for a verified native package.
type Installer struct{ stager *Stager }

// NewInstaller binds the observed CLI/build and compiled key without IO.
func NewInstaller(config StagerConfig) (*Installer, error) {
	stager, err := NewStager(config)
	if err != nil {
		return nil, err
	}

	return &Installer{stager: stager}, nil
}

// PendingInstall owns an executor before commit and lends it independent lifetime afterwards.
type PendingInstall struct {
	command           *exec.Cmd
	log               *os.File
	input             io.WriteCloser
	output            io.ReadCloser
	reader            *bufio.Reader
	request           installRequest
	directory         string
	cancel            context.CancelFunc
	stopOwner         func() bool
	mu                sync.Mutex
	attempted, closed bool
	once, reap        sync.Once
	done              chan struct{}
	closeErr          error
	diagnostics       string
}

// Prepare owns copied package/signature bytes before returning verified Ready.
func (i *Installer) Prepare(ctx context.Context, target MatchRequest, payload *StagedPackage, options InstallOptions) (result *PendingInstall, err error) {
	if err := i.validate(ctx, target, payload, options); err != nil {
		return nil, err
	}

	return i.prepare(ctx, target, payload, options)
}

func (i *Installer) validate(ctx context.Context, target MatchRequest, payload *StagedPackage, options InstallOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if i == nil || i.stager == nil || payload == nil {
		return errors.New("native installer is not configured")
	}

	if err := validateResolved(target, payload.release); err != nil {
		return err
	}

	if target.Installation == InstallPortable {
		return errors.New("portable updates require their own workflow")
	}

	return validateInstallOptions(target, options)
}

func (i *Installer) prepare(ctx context.Context, target MatchRequest, payload *StagedPackage, options InstallOptions) (result *PendingInstall, err error) {
	s := i.stager

	// A finished operation leaves its package and CLI copy for diagnosis. One
	// that cannot be removed yet is tried again by the next installation and
	// must not block this one.
	_ = removeStaleInstallations(s.cacheDir)

	directory, cli, err := prepareStage(ctx, s.cacheDir, s.cliPath)
	if err != nil {
		return nil, err
	}

	defer func() {
		if result == nil {
			err = errors.Join(err, removeOperation(directory))
		}
	}()

	operation, err := prepareStageOperation(ctx, directory, nil)
	if err != nil {
		return nil, err
	}

	if err := copyInstallerPayload(ctx, payload, directory); err != nil {
		return nil, err
	}

	release := payload.release
	release.Release.Name = ""
	release.Release.Notes = ""
	options.Args = slices.Clone(options.Args)
	request := installRequest{Protocol: installProtocol, Operation: operation, BuildVersion: s.version, KeyID: s.key, OwnerPID: os.Getpid(), Target: target, Release: release, Digest: payload.digest, Install: options}

	pending, err := startInstallProcess(ctx, cli, directory, request)
	if err != nil {
		return nil, err
	}

	defer func() {
		if result == nil {
			err = errors.Join(err, pending.Close())
			if pending.diagnostics != "" {
				err = fmt.Errorf("application installer preparation (%s): %w", pending.diagnostics, err)
			}
		}
	}()

	if err := writeInstallFrame(pending.input, request); err != nil {
		return nil, err
	}

	if err := pending.readEvent(ctx, installReady, 2*time.Minute); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return pending, nil
}

func copyInstallerPayload(ctx context.Context, payload *StagedPackage, directory string) error {
	source, err := os.OpenRoot(payload.directory)
	if err != nil {
		return err
	}
	defer source.Close()

	if err := copyRetainedFile(ctx, source, payload.release.Package.Name, directory, payload.release.Package.Name, payload.release.Package.Size); err != nil {
		return err
	}

	return copyRetainedFile(ctx, source, payload.release.Signature.Name, directory, payload.release.Signature.Name, packageSignatureSize)
}

func startInstallProcess(ctx context.Context, cli, directory string, request installRequest) (*PendingInstall, error) {
	// Accepted installation outlives window cancellation; before Commit an explicit
	// owner hook still aborts and joins it. The helper also has its own deadline.
	lifetime, cancel := context.WithTimeout(context.WithoutCancel(ctx), 22*time.Minute)
	command := exec.CommandContext(lifetime, cli, InstallCommand)
	command.Dir = directory
	command.WaitDelay = 3 * time.Second
	hostexec.HideConsole(command)

	p := &PendingInstall{command: command, directory: directory, request: request, cancel: cancel, done: make(chan struct{})}
	if err := p.openPipes(); err != nil {
		cancel()
		return nil, err
	}

	defer func() { _ = p.log.Close() }()

	if err := command.Start(); err != nil {
		_ = p.input.Close()
		_ = p.output.Close()

		cancel()

		return nil, explainHelperStart(err)
	}

	p.mu.Lock()
	p.stopOwner = context.AfterFunc(ctx, func() { _ = p.Close() })
	p.mu.Unlock()

	return p, nil
}

func (p *PendingInstall) openPipes() error {
	root, err := os.OpenRoot(p.directory)
	if err != nil {
		return err
	}
	defer root.Close()

	log, err := root.OpenFile("worker.log", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	p.log = log
	// exec copies this file descriptor into the child. Keep it through Start.
	p.command.Stderr = log
	if err := hostfile.Protect(filepath.Join(p.directory, "worker.log"), false); err != nil {
		_ = log.Close()
		return err
	}

	p.input, err = p.command.StdinPipe()
	if err != nil {
		_ = log.Close()
		return err
	}

	p.output, err = p.command.StdoutPipe()
	if err != nil {
		_ = p.input.Close()
		_ = log.Close()

		return err
	}

	p.reader = bufio.NewReaderSize(p.output, maxStageRequest+1)

	return nil
}

func (p *PendingInstall) readEvent(ctx context.Context, kind string, timeout time.Duration) error {
	bound, cancel := context.WithTimeout(ctx, timeout)

	stop := context.AfterFunc(bound, func() { _ = p.output.Close() })
	defer func() { stop(); cancel() }()

	var event installEvent
	if err := readInstallFrame(p.reader, &event); err != nil {
		return errors.Join(err, ctx.Err(), bound.Err())
	}

	owned := event.Protocol == installProtocol && event.Operation == p.request.Operation && event.BuildVersion == p.request.BuildVersion && event.KeyID == p.request.KeyID
	if owned && event.Kind == installRefused && event.Message != "" {
		return errors.New(event.Message)
	}

	if !owned || event.Kind != kind {
		return errors.New("installer reply does not match its owner")
	}

	return errors.Join(ctx.Err(), bound.Err())
}

// Commit sends one decision; lost acknowledgement never permits a retry.
func (p *PendingInstall) Commit(ctx context.Context) error {
	p.mu.Lock()
	if p.closed || p.attempted {
		p.mu.Unlock()
		return errors.New("installer decision was already consumed")
	}

	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return err
	}

	p.attempted = true
	stop := p.stopOwner
	p.mu.Unlock()

	if stop != nil {
		stop()
	}

	if err := writeInstallFrame(p.input, installDecision{Protocol: installProtocol, Operation: p.request.Operation, Commit: true}); err != nil {
		return err
	}

	return p.readEvent(ctx, installAccepted, 10*time.Second)
}

// Close aborts/joins an uncommitted helper; after an attempt it releases only local handles.
func (p *PendingInstall) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		attempted, stop := p.attempted, p.stopOwner
		p.mu.Unlock()

		if stop != nil {
			stop()
		}

		_ = p.input.Close()

		_ = p.output.Close()
		if !attempted {
			// CommandContext owns process termination; a second Kill can race it
			// and return access denied for an already-terminated Windows process.
			p.cancel()
		}

		p.startReaper()

		if !attempted {
			<-p.done // A killed uncommitted helper has an expected nonzero process exit.
			p.diagnostics = p.readDiagnostics()
			p.closeErr = errors.Join(p.closeErr, removeOperation(p.directory))
		}
	})

	return p.closeErr
}

func (p *PendingInstall) readDiagnostics() string {
	root, err := os.OpenRoot(p.directory)
	if err != nil {
		return ""
	}
	defer root.Close()

	file, err := root.Open("worker.log")
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	var output stageDiagnostics

	_, _ = io.Copy(&output, io.LimitReader(file, 8192))

	return output.String()
}

func (p *PendingInstall) startReaper() {
	p.reap.Do(func() { go func() { _ = p.command.Wait(); p.cancel(); close(p.done) }() })
}
