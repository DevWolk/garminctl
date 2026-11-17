package updatetransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// PackageCommand is the privileged step of a DEB/RPM update. The unprivileged
// helper starts the installed CLI with it through the system authorization
// prompt; it is never a public API.
const PackageCommand = "_application-package"

const packageProtocol = 1

type packageRequest struct {
	Protocol       int          `json:"protocol"`
	BuildVersion   string       `json:"buildVersion"`
	KeyID          string       `json:"keyId"`
	Target         MatchRequest `json:"target"`
	Directory      string       `json:"directory"`
	Size           int64        `json:"size"`
	Digest         string       `json:"digest"`
	AllowDowngrade bool         `json:"allowDowngrade,omitempty"`
}

// PackageWorkerConfig carries only compiled composition, never request-selected trust.
type PackageWorkerConfig struct {
	BuildVersion string
	PublicKey    []byte
}

type packageHost struct {
	query nativeVersionQuery
	run   func(installerInvocation) error
}

// RunPackageWorker installs one signed garminctl package with the system
// package manager. The request names a directory its sender can still write,
// so the package is first copied to a directory only this process can change
// and authenticated there with the compiled key.
func RunPackageWorker(ctx context.Context, config PackageWorkerConfig, input io.Reader, output io.Writer) error {
	return runPackageWorker(ctx, config, input, packageHost{query: queryNativeVersion, run: func(invocation installerInvocation) error {
		// Once started, the OS transaction is not killed by a signal to this step.
		command := exec.Command(invocation.program, invocation.args...) //nolint:gosec,noctx // Fixed package manager and separate argv for the authenticated private copy, no shell. A started OS transaction must finish rather than be killed mid-replacement.
		command.Stdout = output
		command.Stderr = output

		return command.Run()
	}})
}

func runPackageWorker(ctx context.Context, config PackageWorkerConfig, input io.Reader, host packageHost) (err error) {
	request, name, err := readPackageRequest(config, input)
	if err != nil {
		return err
	}

	private, err := os.MkdirTemp("", "garminctl-package-")
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, os.RemoveAll(private)) }()

	payload := AssetRef{Name: name, Size: request.Size}
	if err := copyPackageRequest(ctx, request.Directory, private, payload); err != nil {
		return err
	}

	root, err := os.OpenRoot(private)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := authenticatePackage(ctx, root, payload, name+".sig", request.Digest, config.PublicKey); err != nil {
		return err
	}

	invocation, err := prepareNativeInstaller(ctx, installRequest{Target: request.Target, Release: ResolvedRelease{Package: payload}, Install: InstallOptions{AllowDowngrade: request.AllowDowngrade}}, private, host.query)
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	return host.run(invocation)
}

func readPackageRequest(config PackageWorkerConfig, input io.Reader) (packageRequest, string, error) {
	var request packageRequest

	identity, err := keyID(config.PublicKey)
	if err != nil {
		return request, "", err
	}

	if input == nil {
		return request, "", errors.New("package step requires its request")
	}

	data, err := io.ReadAll(io.LimitReader(input, maxStageRequest+1))
	if err != nil {
		return request, "", err
	}

	if len(data) > maxStageRequest {
		return request, "", errors.New("package request exceeds the byte limit")
	}

	if err := decodeStageJSON(data, &request); err != nil {
		return request, "", err
	}

	if request.Protocol != packageProtocol || request.BuildVersion != config.BuildVersion || request.KeyID != identity {
		return request, "", errors.New("package request build, key or protocol does not match")
	}

	name, err := validatePackageRequest(request)

	return request, name, err
}

// validatePackageRequest returns the publishing filename of the requested package.
func validatePackageRequest(request packageRequest) (string, error) {
	native := request.Target.Installation == InstallDEB || request.Target.Installation == InstallRPM
	if !native || request.Target.OS != runtime.GOOS || request.Target.Arch != runtime.GOARCH {
		return "", errors.New("package step requires a native DEB/RPM package")
	}

	if request.AllowDowngrade && request.Target.Installation != InstallDEB {
		return "", errors.New("downgrade permission is only valid for a DEB rollback")
	}

	digest, err := hex.DecodeString(request.Digest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != request.Digest {
		return "", errors.New("invalid package digest")
	}

	if !filepath.IsAbs(request.Directory) || filepath.Clean(request.Directory) != request.Directory {
		return "", errors.New("invalid package location")
	}

	if request.Size <= 0 || request.Size > maxPackageBytes {
		return "", errors.New("invalid package size")
	}

	return packageFilename(request.Target)
}

func copyPackageRequest(ctx context.Context, directory, private string, payload AssetRef) error {
	source, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open the prepared package: %w", err)
	}
	defer source.Close()

	if err := copyRetainedFile(ctx, source, payload.Name, private, payload.Name, payload.Size); err != nil {
		return err
	}

	return copyRetainedFile(ctx, source, payload.Name+".sig", private, payload.Name+".sig", packageSignatureSize)
}
