package updatetransport

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

// InstallWorkerConfig carries only compiled/local composition, not IPC-selected trust.
type InstallWorkerConfig struct {
	Directory, BuildVersion string
	PublicKey               []byte
	// ProbeMCP is trusted composition's bounded initialize/ping callback, never IPC input.
	ProbeMCP func(context.Context, string, string) error
	// HostCheck replaces the platform's own readiness check for the installation
	// kind, installed CLI and operation directory; nil selects the platform's.
	HostCheck func(InstallKind, string, string) error
}

type installerOwner interface {
	Wait(context.Context) error
	Close() error
}

type installHost struct {
	parentPID int
	openOwner func(int) (installerOwner, error)
	// preflight reports, before Ready, a host condition that would fail the installer after the application has exited.
	preflight func(installRequest, string) error
	run       func(context.Context, installRequest, string) error
	restart   func(context.Context, InstallOptions, string) error
}

// RunInstallWorker owns the bounded helper lifetime; it opens only its real parent.
func RunInstallWorker(ctx context.Context, config InstallWorkerConfig, input io.ReadCloser, output io.Writer) error {
	if config.ProbeMCP == nil {
		if input != nil {
			_ = input.Close()
		}

		return errors.New("installer requires a bounded startup MCP probe")
	}

	preflight := preflightNativeInstall
	if config.HostCheck != nil {
		preflight = func(r installRequest, directory string) error {
			return config.HostCheck(r.Target.Installation, r.Install.CLI, directory)
		}
	}

	return runInstallWorker(ctx, config, input, output, installHost{parentPID: os.Getppid(), openOwner: openInstallerOwner, preflight: preflight, run: runNativeInstaller, restart: func(ctx context.Context, options InstallOptions, tag string) error {
		return restartInstalledDesktop(ctx, options, tag, config.Directory, config.ProbeMCP)
	}})
}

func runInstallWorker(ctx context.Context, config InstallWorkerConfig, input io.ReadCloser, output io.Writer, host installHost) (err error) {
	if input == nil || output == nil {
		return errors.New("installer requires owned input/output pipes")
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	defer func() { _ = input.Close() }()

	stopRead := context.AfterFunc(ctx, func() { _ = input.Close() })
	defer stopRead()

	identity, err := keyID(config.PublicKey)
	if err != nil {
		return err
	}

	reader := bufio.NewReaderSize(input, maxStageRequest+1)

	request, err := readInstallFrameWithin[installRequest](ctx, reader)
	if err != nil {
		return err
	}

	if err := validateInstallRequest(request, config, identity, host.parentPID); err != nil {
		return err
	}

	root, err := os.OpenRoot(config.Directory)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := checkOperationMarker(root, request.Operation); err != nil {
		return err
	}

	if err := authenticateInstallPayload(ctx, root, request, config.PublicKey); err != nil {
		return err
	}

	event := installEvent{Protocol: installProtocol, Operation: request.Operation, BuildVersion: config.BuildVersion, KeyID: identity, Kind: installReady}

	if host.preflight != nil {
		if refusal := host.preflight(request, config.Directory); refusal != nil {
			// Nothing has started: the owner keeps running and shows the reason.
			event.Kind, event.Message = installRefused, boundedInstallMessage(refusal.Error())

			return writeInstallFrame(output, event)
		}
	}

	owner, err := host.openOwner(request.OwnerPID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()

	if err := writeInstallFrame(output, event); err != nil {
		return err
	}

	commit, err := readInstallDecision(ctx, reader, input, request.Operation)
	if err != nil || !commit {
		return err
	}
	defer func() { err = errors.Join(err, writeInstallResult(root, config.Directory, request, err)) }()

	event.Kind = installAccepted
	// Commit may already have been delivered when the parent loses its reply pipe.
	// The parent must report an unknown outcome, while this one executor continues.
	_ = writeInstallFrame(output, event)

	return executeCommittedInstall(ctx, config, root, request, owner, host)
}

func validateInstallRequest(r installRequest, c InstallWorkerConfig, identity string, parent int) error {
	if r.Protocol != installProtocol || r.BuildVersion != c.BuildVersion || r.KeyID != identity || r.OwnerPID <= 0 || r.OwnerPID != parent {
		return errors.New("installer build, key, protocol or parent does not match")
	}

	if r.Target.OS != runtime.GOOS || r.Target.Arch != runtime.GOARCH || r.Target.Installation == InstallPortable {
		return errors.New("installer requires a native Setup/DEB/RPM package")
	}

	if err := validateResolved(r.Target, r.Release); err != nil {
		return err
	}

	digest, err := hex.DecodeString(r.Digest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != r.Digest {
		return errors.New("invalid installer payload digest")
	}

	return validateInstallOptions(r.Target, r.Install)
}

func validateInstallOptions(target MatchRequest, options InstallOptions) error {
	if options.AllowDowngrade && target.Installation != InstallDEB {
		return errors.New("downgrade permission is only valid for a DEB rollback")
	}

	desktop, cli := "garmin-desktop", "garminctl"
	if target.OS == platformWindows {
		desktop += ".exe"
		cli += ".exe"
	}

	if !filepath.IsAbs(options.Desktop) || filepath.Base(options.Desktop) != desktop || !filepath.IsAbs(options.CLI) || filepath.Base(options.CLI) != cli {
		return errors.New("installer restart requires observed native executable paths")
	}

	if target.Installation == InstallSetup {
		if !filepath.IsAbs(options.Root) || filepath.Dir(options.Desktop) != options.Root || filepath.Dir(options.CLI) != options.Root || strings.ContainsAny(options.Root, "\"\x00\r\n") {
			return errors.New("setup requires the observed installation directory")
		}
	}

	return validateRestart(options.Dir, options.Args)
}

func validateRestart(directory string, args []string) error {
	if directory != "" && (!filepath.IsAbs(directory) || strings.ContainsRune(directory, 0)) {
		return errors.New("native restart requires an absolute working directory")
	}

	if len(args) > 8 {
		return errors.New("too many native restart arguments")
	}

	for _, arg := range args {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return errors.New("invalid native restart argument")
		}
	}

	return nil
}

func readInstallDecision(ctx context.Context, reader *bufio.Reader, input io.Closer, operation string) (bool, error) {
	bound, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	stop := context.AfterFunc(bound, func() { _ = input.Close() })
	defer stop()

	decision, err := readInstallFrameWithin[installDecision](bound, reader)
	if err != nil {
		return false, err
	}

	if decision.Protocol != installProtocol || decision.Operation != operation {
		return false, errors.New("installer decision belongs to another operation")
	}

	return decision.Commit, errors.Join(ctx.Err(), bound.Err())
}

// readInstallFrameWithin ends at the deadline even on a pipe that Close cannot
// interrupt; an abandoned read ends with the process. A read that failed
// because the deadline closed the pipe carries the deadline as its cause.
func readInstallFrameWithin[T any](ctx context.Context, reader *bufio.Reader) (T, error) {
	type frame struct {
		value T
		err   error
	}

	done := make(chan frame, 1)

	go func() {
		var read frame

		read.err = readInstallFrame(reader, &read.value)
		done <- read
	}()

	select {
	case read := <-done:
		if read.err != nil {
			read.err = errors.Join(read.err, ctx.Err())
		}

		return read.value, read.err
	case <-ctx.Done():
		var none T

		return none, ctx.Err()
	}
}

func boundedInstallMessage(message string) string {
	if len(message) > 1024 {
		message = message[:1024]
	}

	return message
}

func authenticateInstallPayload(ctx context.Context, root *os.Root, r installRequest, der []byte) error {
	return authenticatePackage(ctx, root, r.Release.Package, r.Release.Signature.Name, r.Digest, der)
}

// authenticatePackage binds the named package to the owner's digest and to the compiled key.
func authenticatePackage(ctx context.Context, root *os.Root, payload AssetRef, signatureName, digest string, der []byte) error {
	key, err := parsePublicKey(der)
	if err != nil {
		return err
	}

	file, err := openPackageFile(root, payload.Name, payload.Size)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if err := copyPackage(ctx, hash, file, payload.Size, nil); err != nil {
		return err
	}

	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("installer bytes changed from verified package")
	}

	signature, err := readStagedSignature(root, signatureName)
	if err != nil {
		return err
	}

	return verifyPackageSignature(ctx, key, hash.Sum(nil), signature)
}

func executeCommittedInstall(ctx context.Context, c InstallWorkerConfig, root *os.Root, r installRequest, owner installerOwner, host installHost) error {
	wait, cancel := context.WithTimeout(ctx, time.Minute)
	err := owner.Wait(wait)

	cancel()

	if err != nil {
		return err
	}

	if err := authenticateInstallPayload(ctx, root, r, c.PublicKey); err != nil {
		return err
	}

	if err := host.run(ctx, r, c.Directory); err != nil {
		return err
	}

	return host.restart(ctx, r.Install, r.Target.ReleaseTag)
}

func writeInstallResult(root *os.Root, directory string, r installRequest, outcome error) error {
	status := InstallSucceeded
	message := ""

	if outcome != nil {
		status = InstallFailed
		message = outcome.Error()
	}

	if errors.Is(outcome, errInstallerOutcomeUnknown) {
		status = InstallUnknown
	}

	if len(message) > 4096 {
		message = message[:4096]
	}

	data, err := json.Marshal(installResultRecord{r.Operation, r.Target.ReleaseTag, status, message})
	if err != nil {
		return err
	}

	file, err := root.OpenFile(".install-result.tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	if err := hostfile.Protect(filepath.Join(directory, ".install-result.tmp"), false); err != nil {
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

	return root.Rename(".install-result.tmp", installResultName)
}
