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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"garminctl/internal/device"
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

func scriptPath() (string, error) {
	dir := filepath.Join(os.TempDir(), "garminctl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "wpd.ps1")
	if cur, err := os.ReadFile(p); err != nil || string(cur) != script {
		if err := os.WriteFile(p, []byte(script), 0o644); err != nil {
			return "", err
		}
	}
	if runtime.GOOS == "windows" {
		return p, nil
	}
	out, err := exec.Command("wslpath", "-w", p).Output()
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
	sp, err := scriptPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", sp)
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
		b.Close()
		return nil, fmt.Errorf("bridge did not start: %w (%s)", err, strings.TrimSpace(b.stderr.String()))
	}
	if !resp.Ready {
		b.Close()
		return nil, fmt.Errorf("bridge did not report ready: %s", resp.Error)
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

// Call sends one request and waits for its response.
func (b *Bridge) Call(ctx context.Context, req map[string]any) (*response, error) {
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
	defer b.Close()
	resp, err := b.Call(ctx, map[string]any{"op": "devices"})
	if err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

// Device is an opened MTP device.
type Device struct {
	mu         sync.Mutex
	bridge     *Bridge
	info       device.Info
	reconnects int
}

// Open connects to the device with the given ID. An empty ID selects the
// first Garmin device, or the only device present.
func Open(ctx context.Context, id string) (*Device, error) {
	b, err := Start(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := b.Call(ctx, map[string]any{"op": "devices"})
	if err != nil {
		b.Close()
		return nil, err
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
		b.Close()
		if len(resp.Devices) == 0 {
			return nil, errors.New("no portable device connected; plug the watch in over USB")
		}
		return nil, fmt.Errorf("device %q not found among %d portable devices", id, len(resp.Devices))
	}
	if _, err := b.Call(ctx, map[string]any{"op": "open", "device": info.ID}); err != nil {
		b.Close()
		return nil, fmt.Errorf("open %s: %w", info.Name, err)
	}
	return &Device{bridge: b, info: info}, nil
}

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
	resp, err := b.Call(ctx, cloneReq(req))
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
	return b.Call(ctx, cloneReq(req))
}

func cloneReq(req map[string]any) map[string]any {
	c := make(map[string]any, len(req)+1)
	for k, v := range req {
		c[k] = v
	}
	return c
}

func (d *Device) List(ctx context.Context, path string) ([]device.Entry, error) {
	resp, err := d.call(ctx, map[string]any{"op": "list", "path": path})
	if err != nil {
		return nil, err
	}
	if resp.Entries == nil {
		return []device.Entry{}, nil
	}
	return resp.Entries, nil
}

func (d *Device) Stat(ctx context.Context, path string) (device.Entry, error) {
	resp, err := d.call(ctx, map[string]any{"op": "stat", "path": path})
	if err != nil {
		return device.Entry{}, err
	}
	if resp.Entry == nil {
		return device.Entry{}, device.ErrNotFound
	}
	return *resp.Entry, nil
}

func (d *Device) Read(ctx context.Context, path string) ([]byte, error) {
	resp, err := d.call(ctx, map[string]any{"op": "read", "path": path})
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(resp.B64)
}

func (d *Device) Write(ctx context.Context, path string, data []byte) error {
	_, err := d.call(ctx, map[string]any{"op": "write", "path": path, "b64": base64.StdEncoding.EncodeToString(data)})
	return err
}

func (d *Device) Delete(ctx context.Context, path string) error {
	_, err := d.call(ctx, map[string]any{"op": "delete", "path": path})
	return err
}

func (d *Device) Mkdir(ctx context.Context, path string) error {
	_, err := d.call(ctx, map[string]any{"op": "mkdir", "path": path})
	return err
}

func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bridge.Close()
}
