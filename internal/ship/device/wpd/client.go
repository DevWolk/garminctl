// Package wpd reaches MTP devices through a PowerShell process that hosts a
// small C# helper on top of the Windows Portable Devices COM API. It works
// from Windows and from WSL (through powershell.exe interop).
package wpd

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/hostexec"
	"github.com/DevWolk/garminctl/internal/ship/paths"
)

//go:embed wpd.ps1
var script string

// Bridge is a running PowerShell bridge process.
type Bridge struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	out         *bufio.Reader
	stderr      *bridgeDiagnostics
	mu          sync.Mutex
	nextID      int
	closed      atomic.Bool
	workers     sync.Once
	stopOnce    sync.Once
	closeOnce   sync.Once
	stop        chan struct{}
	responses   chan response
	readerDone  chan struct{}
	processDone chan struct{}
	readErr     error // published by closing responses
	waitErr     error // published by closing processDone
	closeErr    error // published by closeOnce
}

type response struct {
	ID    int    `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	// NotFound marks the bridge's own finding that the path does not exist.
	NotFound bool            `json:"notFound"`
	Ready    bool            `json:"ready"`
	Devices  []device.Info   `json:"devices"`
	Entries  []device.Entry  `json:"entries"`
	Entry    *device.Entry   `json:"entry"`
	B64      string          `json:"b64"`
	Code     uint32          `json:"code"`
	Params   []uint32        `json:"params"`
	Object   string          `json:"objectId"`
	Storage  string          `json:"storage"`
	Raw      json.RawMessage `json:"-"`
}

// PowerShellPath returns the path of powershell.exe, or "" when unavailable.
func PowerShellPath() string {
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			return p
		}

		return ""
	}

	candidates := []string{
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"/mnt/c/Windows/Sysnative/WindowsPowerShell/v1.0/powershell.exe",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}

// Available reports whether the bridge can run on this host.
func Available() bool { return PowerShellPath() != "" }

// scriptPath writes the bridge script where PowerShell can read it and
// returns that path in Windows form. It lives in the user's own cache
// directory, created 0700: a shared temp directory would let another local
// account plant a symlink or swap the script between our write and
// PowerShell reading it. os.Root refuses a symlink pointing out of the
// directory, so the script is only ever written where we expect it.
func scriptPath(ctx context.Context) (string, error) {
	dir := paths.Cache()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()

	const name = "wpd.ps1"

	cur, err := root.ReadFile(name)
	if err != nil || string(cur) != script {
		if err := root.WriteFile(name, []byte(script), 0o600); err != nil {
			return "", err
		}
	}

	p := filepath.Join(dir, name)

	if runtime.GOOS == "windows" {
		return p, nil
	}

	out, err := exec.CommandContext(ctx, "wslpath", "-w", p).Output() //nolint:gosec // argv is fixed; p is our script path
	if err != nil {
		return "", fmt.Errorf("wslpath: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// Start launches the bridge process and waits for it to be ready.
func Start(ctx context.Context) (*Bridge, error) {
	ps := PowerShellPath()
	if ps == "" {
		return nil, errors.New("powershell.exe not found; the MTP bridge needs Windows or WSL")
	}

	sp, err := scriptPath(ctx)
	if err != nil {
		return nil, err
	}

	// The bridge outlives ctx (which only bounds the start-up); Close stops it.
	//nolint:gosec // argv is fixed: ps is powershell.exe as found by PowerShellPath, sp our script file
	cmd := exec.CommandContext(context.WithoutCancel(ctx), ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-File", sp)
	hostexec.HideConsole(cmd)

	b, err := startBridge(cmd)
	if err != nil {
		return nil, err
	}

	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := b.readResponse(readyCtx)
	if err != nil {
		return nil, fmt.Errorf("bridge did not start: %w", b.fail(errors.Join(readyCtx.Err(), err)))
	}

	if !resp.Ready || !resp.OK || resp.ID != 0 {
		return nil, b.fail(errors.New("bridge did not report ready"))
	}

	return b, nil
}

func asciiJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var sb strings.Builder

	for _, r := range string(data) {
		if r < 128 {
			sb.WriteRune(r)
			continue
		}

		if r > 0xFFFF {
			r -= 0x10000
			fmt.Fprintf(&sb, "\\u%04x\\u%04x", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		} else {
			fmt.Fprintf(&sb, "\\u%04x", r)
		}
	}

	return []byte(sb.String()), nil
}

// call sends one request and waits for its response.
func (b *Bridge) call(ctx context.Context, req map[string]any) (*response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := validateRequestPath(req); err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if b.closed.Load() {
		return nil, fmt.Errorf("%w: bridge closed", device.ErrDisconnected)
	}

	b.nextID++
	req["id"] = b.nextID

	data, err := asciiJSON(req)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if b.closed.Load() {
		return nil, fmt.Errorf("%w: bridge closed", device.ErrDisconnected)
	}

	b.startWorkers()
	// Admission arms cancellation for blocked pipe writes as well as reads.
	// abort does not take mu, which is held throughout this exchange.
	stop := context.AfterFunc(ctx, b.abort)
	defer func() {
		if !stop() {
			// Finish an already scheduled abort before releasing the call lock.
			b.abort()
		}
	}()

	return b.exchange(ctx, append(data, '\n'))
}

// exchange runs with mu held and the caller's cancellation hook armed.
func (b *Bridge) exchange(ctx context.Context, data []byte) (*response, error) {
	n, err := b.stdin.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}

	if err != nil {
		return nil, b.fail(errors.Join(ctx.Err(), fmt.Errorf("bridge write: %w", err)))
	}

	resp, err := b.readResponse(ctx)
	if err != nil {
		return nil, b.fail(errors.Join(ctx.Err(), err))
	}

	if resp.ID != b.nextID || resp.Ready {
		return nil, b.fail(errors.Join(ctx.Err(), errors.New("bridge response ID does not match request")))
	}

	if err := ctx.Err(); err != nil {
		return nil, b.fail(err)
	}

	if !resp.OK {
		err := remoteError(resp)
		if errors.Is(err, device.ErrDisconnected) {
			return nil, b.fail(err)
		}

		return nil, err
	}

	return resp, nil
}

// remoteError classifies a failed bridge reply. Absence comes only from the
// bridge's flag: other failures can mention "not found" in their text.
func remoteError(resp *response) error {
	message := resp.Error

	err := errors.New(message)
	if strings.Contains(message, "0x800700AA") || strings.Contains(message, "0x80070964") {
		return fmt.Errorf("%w: %w", device.ErrBusy, err)
	}

	if staleHRESULT(message) {
		return fmt.Errorf("%w: %w", device.ErrDisconnected, err)
	}

	if resp.NotFound {
		return fmt.Errorf("%w: %w", device.ErrNotFound, err)
	}

	if isStale(err) {
		return fmt.Errorf("%w: %w", device.ErrDisconnected, err)
	}

	return err
}

func validateRequestPath(req map[string]any) error {
	path, _ := req["path"].(string)
	if slices.Contains(strings.Split(strings.ReplaceAll(path, "\\", "/"), "/"), "..") {
		return fmt.Errorf("%w: invalid watch path", device.ErrInvalidArgument)
	}

	return nil
}

// Devices lists the portable devices Windows currently sees.
func Devices(ctx context.Context) ([]device.Info, error) {
	b, err := Start(ctx)
	if err != nil {
		return nil, err
	}

	list, err := b.devices(ctx)
	if err != nil {
		return nil, errors.Join(err, b.Close())
	}

	return list, b.Close()
}

// devices asks the bridge which portable devices Windows sees.
func (b *Bridge) devices(ctx context.Context) ([]device.Info, error) {
	resp, err := b.call(ctx, map[string]any{"op": "devices"})
	if err != nil {
		return nil, err
	}

	return trimInfo(resp.Devices), nil
}

// trimInfo removes padding from device names and manufacturers. IDs retain
// their original bytes for exact -device matching.
func trimInfo(list []device.Info) []device.Info {
	for i := range list {
		d := &list[i]
		d.Name, d.Manufacturer, d.Description = strings.TrimSpace(d.Name), strings.TrimSpace(d.Manufacturer), strings.TrimSpace(d.Description)
	}

	return list
}

// Device is an opened MTP device.
type Device struct {
	mu           sync.Mutex
	bridge       *Bridge
	info         device.Info
	reconnects   int
	open         func(context.Context, string) (*Device, error)
	closed       bool
	reopening    chan struct{}
	cancelReopen context.CancelFunc
	noReconnect  bool
}

var (
	_ device.Device          = (*Device)(nil)
	_ device.HiddenFS        = (*Device)(nil)
	_ device.StorageReporter = (*Device)(nil)
	_ device.Aborter         = (*Device)(nil)
	_ device.ReconnectPolicy = (*Device)(nil)
)

// Open connects to the device with the given ID. An empty ID selects the
// first Garmin device and nothing else; see pick.
func Open(ctx context.Context, id string) (*Device, error) {
	b, err := Start(ctx)
	if err != nil {
		return nil, err
	}

	list, err := b.devices(ctx)
	if err != nil {
		return nil, errors.Join(err, b.Close())
	}

	info, err := pick(list, id)
	if err != nil {
		return nil, errors.Join(err, b.Close())
	}

	if _, err := b.call(ctx, map[string]any{"op": "open", "device": info.ID}); err != nil {
		return nil, errors.Join(fmt.Errorf("open %s: %w", info.Name, err), b.Close())
	}

	return &Device{bridge: b, info: info, open: Open}, nil
}

// pick chooses the device to open from what Windows lists. An explicit id
// must match exactly. An empty id takes the first Garmin device, recognised
// by its manufacturer or by USB vendor 091E in the device path. No other
// device qualifies: Windows lists phones and USB drives through the same
// API, and a USB drive must not be taken for the watch.
func pick(devices []device.Info, id string) (device.Info, error) {
	if len(devices) == 0 {
		return device.Info{}, errors.New("no portable device connected; plug the watch in over USB")
	}

	for _, d := range devices {
		if matches(d, id) {
			return d, nil
		}
	}

	if id != "" {
		return device.Info{}, fmt.Errorf("device %q not found among %d portable devices", id, len(devices))
	}

	names := make([]string, 0, len(devices))
	for _, d := range devices {
		names = append(names, describe(d))
	}

	return device.Info{}, fmt.Errorf("no Garmin device among %d portable device(s): %s", len(devices), strings.Join(names, ", "))
}

func matches(d device.Info, id string) bool {
	if id != "" {
		return d.ID == id
	}

	return strings.EqualFold(d.Manufacturer, "Garmin") || strings.Contains(strings.ToLower(d.ID), "vid_091e")
}

func describe(d device.Info) string {
	if d.Manufacturer == "" {
		return d.Name
	}

	return d.Name + " (" + d.Manufacturer + ")"
}

// Info implements device.Device.
func (d *Device) Info() device.Info { return d.info }

// Reconnects counts how often the device was reopened; callers use it to
// drop caches after the watch was unplugged and plugged back in.
func (d *Device) Reconnects() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.reconnects
}

// isStale reports errors that mean the device object is gone: the watch was
// unplugged (Windows shuts the object down) or the bridge process died.
func isStale(err error) bool {
	if err == nil || errors.Is(err, device.ErrNotFound) || errors.Is(err, device.ErrBusy) {
		return false
	}

	if errors.Is(err, device.ErrDisconnected) {
		return true
	}

	m := err.Error()

	return staleHRESULT(m) || strings.Contains(m, "Shutdown was already called") || strings.Contains(m, "is not connected")
}

func staleHRESULT(message string) bool {
	message = strings.ToUpper(message)
	for _, code := range []string{"0X802A0002", "0X8007048F", "0X800706BA", "0X8007001F"} {
		if strings.Contains(message, code) {
			return true
		}
	}

	return false
}

// call permits one reopen/retry for independent reads addressed by path.
// Mutations and raw handle/opcode diagnostics never replay an unknown outcome.
func (d *Device) call(ctx context.Context, req map[string]any) (*response, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, fmt.Errorf("%w: WPD device closed", device.ErrDisconnected)
	}

	b := d.bridge
	noReconnect := d.noReconnect
	d.mu.Unlock()

	resp, err := b.call(ctx, cloneReq(req))
	if !isStale(err) {
		return resp, err
	}

	if noReconnect || !retryablePathRead(req) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}

	b, oerr := d.reopen(ctx, b)
	if oerr != nil {
		return nil, fmt.Errorf("%w: the watch connection was lost and could not be reopened: %w", device.ErrDisconnected, errors.Join(err, oerr))
	}

	return b.call(ctx, cloneReq(req))
}

// SetAutoReconnect configures future calls. Managed operation groups disable it
// before publishing a newly opened device so a read cannot cross into a new epoch.
func (d *Device) SetAutoReconnect(enabled bool) {
	d.mu.Lock()
	d.noReconnect = !enabled
	d.mu.Unlock()
}

// Abort interrupts a pipe exchange or reconnect startup without waiting for it.
func (d *Device) Abort() {
	d.mu.Lock()

	d.closed = true
	if d.cancelReopen != nil {
		d.cancelReopen()
	}

	b := d.bridge
	d.mu.Unlock()
	b.abort()
}

func retryablePathRead(req map[string]any) bool {
	op, _ := req["op"].(string)
	switch op {
	case "list", "stat", "read", "objectid":
		return true
	default:
		return false
	}
}

// pathReq is a bridge request that names a path on the watch.
func pathReq(op, path string) map[string]any {
	return map[string]any{"op": op, "path": path}
}

func cloneReq(req map[string]any) map[string]any {
	c := make(map[string]any, len(req)+1)
	maps.Copy(c, req)

	return c
}

// List implements device.Device.
func (d *Device) List(ctx context.Context, path string) ([]device.Entry, error) {
	resp, err := d.call(ctx, pathReq("list", path))
	if err != nil {
		return nil, err
	}

	// The device names its own objects. A name with a separator or a dot
	// entry would steer a caller (backup, the web UI) outside the folder it
	// asked for, so such entries are dropped.
	entries := make([]device.Entry, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		if validName(e.Name) {
			entries = append(entries, e)
		}
	}

	return entries, nil
}

// validName accepts one path element as a device may report it.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// Stat implements device.Device.
func (d *Device) Stat(ctx context.Context, path string) (device.Entry, error) {
	resp, err := d.call(ctx, pathReq("stat", path))
	if err != nil {
		return device.Entry{}, err
	}

	if resp.Entry == nil {
		return device.Entry{}, device.ErrNotFound
	}

	return *resp.Entry, nil
}

// Read implements device.Device.
func (d *Device) Read(ctx context.Context, path string) ([]byte, error) {
	resp, err := d.call(ctx, pathReq("read", path))
	if err != nil {
		return nil, err
	}

	return base64.StdEncoding.DecodeString(resp.B64)
}

// Write implements device.Device.
func (d *Device) Write(ctx context.Context, path string, data []byte) error {
	req := pathReq("write", path)
	req["b64"] = base64.StdEncoding.EncodeToString(data)
	_, err := d.call(ctx, req)

	return err
}

// Delete implements device.Device.
func (d *Device) Delete(ctx context.Context, path string) error {
	_, err := d.call(ctx, pathReq("delete", path))
	return err
}

// Mkdir implements device.Device.
func (d *Device) Mkdir(ctx context.Context, path string) error {
	_, err := d.call(ctx, pathReq("mkdir", path))
	return err
}

// ObjectID returns the WPD object id of a path and the storage id, as the
// Windows MTP driver names them ("o<hex handle>", "s<hex storage id>").
func (d *Device) ObjectID(ctx context.Context, path string) (object, storage string, err error) {
	resp, err := d.call(ctx, pathReq("objectid", path))
	if err != nil {
		return "", "", err
	}

	return resp.Object, resp.Storage, nil
}

// ListByID enumerates the children of a raw WPD object id, bypassing path resolution.
func (d *Device) ListByID(ctx context.Context, objectID string) ([]device.Entry, error) {
	resp, err := d.call(ctx, map[string]any{"op": "listid", "objectId": objectID})
	if err != nil {
		return nil, err
	}

	return resp.Entries, nil
}

// MtpRead runs a raw MTP operation that answers with a data phase.
func (d *Device) MtpRead(ctx context.Context, opcode uint32, params []uint32) (data []byte, code uint32, rparams []uint32, err error) {
	resp, err := d.call(ctx, map[string]any{"op": "mtpread", "opcode": opcode, "params": params})
	if err != nil {
		return nil, 0, nil, err
	}

	b, err := base64.StdEncoding.DecodeString(resp.B64)

	return b, resp.Code, resp.Params, errors.Join(err, ptpStatusError(resp.Code))
}

// MtpWrite runs a raw MTP operation that sends a data phase to the device.
func (d *Device) MtpWrite(ctx context.Context, opcode uint32, params []uint32, data []byte) (code uint32, rparams []uint32, err error) {
	resp, err := d.call(ctx, map[string]any{"op": "mtpwrite", "opcode": opcode, "params": params, "b64": base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return 0, nil, err
	}

	return resp.Code, resp.Params, ptpStatusError(resp.Code)
}

// MtpNoData runs a raw MTP operation without a data phase.
func (d *Device) MtpNoData(ctx context.Context, opcode uint32, params []uint32) (code uint32, rparams []uint32, err error) {
	resp, err := d.call(ctx, map[string]any{"op": "mtpnodata", "opcode": opcode, "params": params})
	if err != nil {
		return 0, nil, err
	}

	return resp.Code, resp.Params, ptpStatusError(resp.Code)
}

// Close implements device.Device.
func (d *Device) Close() error {
	d.mu.Lock()

	d.closed = true
	if d.cancelReopen != nil {
		d.cancelReopen()
	}

	b, reopening := d.bridge, d.reopening
	d.mu.Unlock()

	err := b.Close()

	if reopening != nil {
		timer := time.NewTimer(bridgeCleanupTimeout)
		defer timer.Stop()

		select {
		case <-reopening:
		case <-timer.C:
			err = errors.Join(err, errors.New("WPD reopen cleanup timed out"))
		}
	}

	return err
}
