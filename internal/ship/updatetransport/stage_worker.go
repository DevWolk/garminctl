package updatetransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/DevWolk/garminctl/internal/hostfile"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// WorkerConfig is supplied by compiled CLI composition, never decoded from IPC.
type WorkerConfig struct {
	Directory, BuildVersion string
	PublicKey               []byte
	HTTPClient              *http.Client
}

// RunStageWorker stages only in a marked private operation directory. The key
// comes from the build; the request can only require its expected fingerprint.
func RunStageWorker(ctx context.Context, config WorkerConfig, input io.Reader, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	identity, err := keyID(config.PublicKey)
	if err != nil {
		return err
	}

	request, err := readStageRequest(input)
	if err != nil {
		return err
	}

	if request.BuildVersion != config.BuildVersion || request.KeyID != identity {
		return errors.New("cached CLI build or compiled key does not match its owner")
	}

	if request.Target.OS != runtime.GOOS || request.Target.Arch != runtime.GOARCH {
		return errors.New("staging requires the native CLI platform")
	}

	provider, err := NewProvider(config.HTTPClient, request.Target, request.Release, config.PublicKey)
	if err != nil {
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	host := &stageHost{request: request, encode: json.NewEncoder(output), cancel: cancel}

	snapshot, err := root.OpenFile(request.Release.Package.Name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = snapshot.Close() }() // successful Sync/Close is checked before the receipt

	mirror, flow, err := downloadWorkerStage(ctx, config, request, provider, snapshot, host)
	if err != nil {
		return err
	}

	return completeWorkerStage(ctx, config, request, identity, root, snapshot, mirror, flow, host)
}

func downloadWorkerStage(ctx context.Context, config WorkerConfig, request stageRequest, provider *Provider, snapshot io.Writer, host *stageHost) (*stageMirror, *updater.Updater, error) {
	mirror := &stageMirror{Provider: provider, snapshot: snapshot, digest: sha256.New()}

	flow := updater.New(host)
	if err := flow.Init(updater.Config{
		CurrentVersion: config.BuildVersion, Platform: request.Target.OS, Arch: request.Target.Arch,
		Providers: []updater.Provider{mirror}, Window: updater.WindowNone,
	}); err != nil {
		return nil, nil, err
	}

	if _, err := flow.Check(ctx); err != nil {
		return nil, nil, errors.Join(err, host.err)
	}

	if err := flow.DownloadAndInstall(ctx); err != nil {
		return nil, nil, errors.Join(err, host.err)
	}

	return mirror, flow, nil
}

func completeWorkerStage(ctx context.Context, config WorkerConfig, request stageRequest, identity string, root *os.Root, snapshot *os.File, mirror *stageMirror, flow *updater.Updater, host *stageHost) error {
	if err := errors.Join(ctx.Err(), host.err); err != nil {
		return err
	}

	if err := snapshot.Sync(); err != nil {
		return err
	}

	if err := snapshot.Close(); err != nil {
		return err
	}

	if err := root.WriteFile(request.Release.Signature.Name, mirror.signature, 0o600); err != nil {
		return err
	}

	if err := removeWorkerStage(root, config.Directory, flow.DownloadedPath()); err != nil {
		return err
	}

	for _, name := range []string{request.Release.Package.Name, request.Release.Signature.Name} {
		if err := hostfile.Protect(filepath.Join(config.Directory, name), false); err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	return host.encode.Encode(stageEvent{
		Protocol: stageProtocol, Operation: request.Operation, Kind: "complete",
		Written: request.Release.Package.Size, Total: request.Release.Package.Size,
		BuildVersion: config.BuildVersion, KeyID: identity, Digest: hex.EncodeToString(mirror.digest.Sum(nil)),
	})
}

func checkOperationMarker(root *os.Root, operation string) error {
	file, err := root.Open(operationMarker)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() || info.Size() != 64 {
		return errors.New("stager requires a private operation marker")
	}

	value, err := io.ReadAll(io.LimitReader(file, 65))
	if err != nil {
		return err
	}

	if string(value) != operation {
		return errors.New("staging operation marker does not match")
	}

	return nil
}

func removeWorkerStage(root *os.Root, directory, path string) error {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return err
	}

	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "wails-update-") {
		return errors.New("wails staging path escaped its operation")
	}

	return root.RemoveAll(parts[0])
}

type stageMirror struct {
	*Provider
	snapshot  io.Writer
	digest    hash.Hash
	signature []byte
}

func (p *stageMirror) Download(ctx context.Context, release *updater.Release, dst io.Writer, progress func(int64, int64)) error {
	signature, err := p.download(ctx, release, io.MultiWriter(dst, p.snapshot, p.digest), progress)
	if err == nil {
		p.signature = signature
	}

	return err
}

type stageHost struct {
	mu      sync.Mutex
	request stageRequest
	encode  *json.Encoder
	cancel  context.CancelFunc
	err     error
}

func (h *stageHost) Emit(name string, values ...any) bool {
	if name != updater.EventDownloadProgress || len(values) != 1 {
		return true
	}

	value, ok := values[0].(updater.Progress)
	if !ok {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.err != nil {
		return false
	}

	h.err = h.encode.Encode(stageEvent{Protocol: stageProtocol, Operation: h.request.Operation, Kind: "progress", Written: value.Written, Total: value.Total})
	if h.err != nil {
		h.cancel()
		return false
	}

	return true
}

func (*stageHost) OnEvent(string, func(any)) func() { return func() {} }
func (h *stageHost) OpenWindow(updater.WindowOptions) updater.WindowHandle {
	h.err = errors.New("stager cannot open a native window")
	h.cancel()

	return nil
}

func (h *stageHost) Quit() { h.err = errors.New("stager cannot restart or quit its owner"); h.cancel() }
