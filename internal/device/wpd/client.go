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
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/paths"
)

//go:embed wpd.ps1
var script string

// Bridge is a running PowerShell bridge process.
type Bridge struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	stderr *strings.Builder
	mu     sync.Mutex
	nextID int
	closed bool
}

type response struct {
	ID      int             `json:"id"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error"`
	Ready   bool            `json:"ready"`
	Devices []device.Info   `json:"devices"`
	Entries []device.Entry  `json:"entries"`
	Entry   *device.Entry   `json:"entry"`
	B64     string          `json:"b64"`
	Code    uint32          `json:"code"`
	Params  []uint32        `json:"params"`
	Object  string          `json:"objectId"`
	Storage string          `json:"storage"`
	Raw     json.RawMessage `json:"-"`
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
	cmd := exec.CommandContext(context.WithoutCancel(ctx), ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", sp)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	b := &Bridge{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, 1<<20), stderr: &strings.Builder{}}

	cmd.Stderr = b.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := b.readResponse(readyCtx)
	if err != nil {
		err = fmt.Errorf("bridge did not start: %w (%s)", err, strings.TrimSpace(b.stderr.String()))
		return nil, errors.Join(err, b.Close())
	}

	if !resp.Ready {
		return nil, errors.Join(fmt.Errorf("bridge did not report ready: %s", resp.Error), b.Close())
	}

	return b, nil
}

func (b *Bridge) readResponse(ctx context.Context) (*response, error) {
	type result struct {
		line []byte
		err  error
	}

	ch := make(chan result, 1)

	go func() {
		line, err := b.out.ReadBytes('\n')
		ch <- result{line, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil && len(r.line) == 0 {
			return nil, fmt.Errorf("bridge exited: %w", r.err)
		}

		var resp response
		if err := json.Unmarshal(r.line, &resp); err != nil {
			return nil, fmt.Errorf("bridge sent invalid JSON: %q", strings.TrimSpace(string(r.line)))
		}

		return &resp, nil
	}
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
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, errors.New("bridge closed")
	}

	b.nextID++
	req["id"] = b.nextID

	data, err := asciiJSON(req)
	if err != nil {
		return nil, err
	}

	if _, err := b.stdin.Write(append(data, '\n')); err != nil {
		b.closed = true
		return nil, fmt.Errorf("bridge write: %w", err)
	}

	resp, err := b.readResponse(ctx)
	if err != nil {
		b.closed = true
		return nil, err
	}

	if !resp.OK {
		if strings.Contains(resp.Error, "not found") {
			return nil, fmt.Errorf("%w: %s", device.ErrNotFound, resp.Error)
		}

		return nil, errors.New(resp.Error)
	}

	return resp, nil
}

// Close stops the bridge process.
func (b *Bridge) Close() error {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		_, _ = b.stdin.Write([]byte(`{"id":0,"op":"quit"}` + "\n"))
		_ = b.stdin.Close()
	}
	b.mu.Unlock()

	done := make(chan struct{})

	go func() { _ = b.cmd.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = b.cmd.Process.Kill()
	}

	return nil
}

// Devices lists the portable devices Windows currently sees.
func Devices(ctx context.Context) ([]device.Info, error) {
	b, err := Start(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := b.call(ctx, map[string]any{"op": "devices"})
	if err != nil {
		return nil, errors.Join(err, b.Close())
	}

	return resp.Devices, b.Close()
}

// Device is an opened MTP device.
type Device struct {
	mu         sync.Mutex
	bridge     *Bridge
	info       device.Info
	reconnects int
}

var (
	_ device.Device          = (*Device)(nil)
	_ device.HiddenFS        = (*Device)(nil)
	_ device.StorageReporter = (*Device)(nil)
)

// Open connects to the device with the given ID. An empty ID selects the
// first Garmin device, or the only device present.
func Open(ctx context.Context, id string) (*Device, error) {
	b, err := Start(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := b.call(ctx, map[string]any{"op": "devices"})
	if err != nil {
		return nil, errors.Join(err, b.Close())
	}

	var info device.Info

	for _, d := range resp.Devices {
		if id != "" && d.ID == id {
			info = d
			break
		}

		if id == "" && (strings.EqualFold(d.Manufacturer, "Garmin") || strings.Contains(strings.ToLower(d.ID), "vid_091e")) {
			info = d
			break
		}
	}

	if info.ID == "" && id == "" && len(resp.Devices) == 1 {
		info = resp.Devices[0]
	}

	if info.ID == "" {
		err := fmt.Errorf("device %q not found among %d portable devices", id, len(resp.Devices))
		if len(resp.Devices) == 0 {
			err = errors.New("no portable device connected; plug the watch in over USB")
		}

		return nil, errors.Join(err, b.Close())
	}

	if _, err := b.call(ctx, map[string]any{"op": "open", "device": info.ID}); err != nil {
		return nil, errors.Join(fmt.Errorf("open %s: %w", info.Name, err), b.Close())
	}

	return &Device{bridge: b, info: info}, nil
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
	if err == nil || errors.Is(err, device.ErrNotFound) {
		return false
	}

	m := err.Error()

	return strings.Contains(m, "0x802A0002") || strings.Contains(m, "Shutdown was already called") ||
		strings.Contains(m, "0x8007048F") || strings.Contains(m, "is not connected") ||
		strings.Contains(m, "0x800706BA") || strings.Contains(m, "0x8007001F") ||
		strings.Contains(m, "bridge closed") || strings.Contains(m, "bridge write") || strings.Contains(m, "bridge exited")
}

// call runs one bridge op; when the device object turned out to be stale it
// reopens the same watch once and retries.
func (d *Device) call(ctx context.Context, req map[string]any) (*response, error) {
	d.mu.Lock()
	b := d.bridge
	d.mu.Unlock()

	resp, err := b.call(ctx, cloneReq(req))
	if !isStale(err) {
		return resp, err
	}

	d.mu.Lock()
	if d.bridge == b { // nobody else reopened it yet
		_ = b.Close()

		fresh, oerr := Open(ctx, d.info.ID)
		if oerr != nil {
			d.mu.Unlock()
			return nil, fmt.Errorf("the watch connection was lost and could not be reopened: %w", oerr)
		}

		d.bridge = fresh.bridge
		d.reconnects++
	}

	b = d.bridge
	d.mu.Unlock()

	return b.call(ctx, cloneReq(req))
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

	return b, resp.Code, resp.Params, err
}

// MtpWrite runs a raw MTP operation that sends a data phase to the device.
func (d *Device) MtpWrite(ctx context.Context, opcode uint32, params []uint32, data []byte) (code uint32, rparams []uint32, err error) {
	resp, err := d.call(ctx, map[string]any{"op": "mtpwrite", "opcode": opcode, "params": params, "b64": base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return 0, nil, err
	}

	return resp.Code, resp.Params, nil
}

// MtpNoData runs a raw MTP operation without a data phase.
func (d *Device) MtpNoData(ctx context.Context, opcode uint32, params []uint32) (code uint32, rparams []uint32, err error) {
	resp, err := d.call(ctx, map[string]any{"op": "mtpnodata", "opcode": opcode, "params": params})
	if err != nil {
		return 0, nil, err
	}

	return resp.Code, resp.Params, nil
}

// Close implements device.Device.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.bridge.Close()
}
