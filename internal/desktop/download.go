package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

// This is a local transfer policy, not a measured watch/OS limit. A host may
// configure a different positive timeout without changing file-export routes.
const defaultDownloadTimeout = 10 * time.Minute

// nativeDownloads owns only native file exports. Watch/session/job ownership
// remains in the shared backend; a reused backend is never stopped here.
type nativeDownloads struct {
	mu      sync.Mutex
	ctx     context.Context //nolint:containedctx // derived once from the native window lifetime and cancelled/joined on shutdown
	stop    context.CancelFunc
	closed  bool
	active  sync.WaitGroup
	client  *http.Client
	timeout time.Duration
}

func (d *nativeDownloads) startup(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.closed && d.ctx == nil {
		d.ctx, d.stop = context.WithCancel(ctx)
	}
}

func (d *nativeDownloads) windowContext() (context.Context, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.contextLocked()
}

func (d *nativeDownloads) contextLocked() (context.Context, error) {
	if d.closed {
		return nil, context.Canceled
	}

	if d.ctx == nil {
		return nil, errors.New("desktop window is not ready")
	}

	return d.ctx, d.ctx.Err()
}

func (d *nativeDownloads) save(u *url.URL, target string) error {
	d.mu.Lock()

	ctx, err := d.contextLocked()
	if err != nil {
		d.mu.Unlock()
		return err
	}

	timeout := d.timeout
	if timeout <= 0 {
		timeout = defaultDownloadTimeout
	}

	if d.client == nil {
		d.client = newDownloadClient(timeout)
	}

	client := d.client
	d.active.Add(1)

	d.mu.Unlock()
	defer d.active.Done()

	transferCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return saveDownload(transferCtx, client, u, target)
}

func newDownloadClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport
	if t, ok := transport.(*http.Transport); ok {
		transport = t.Clone()
	}

	return &http.Client{Timeout: timeout, Transport: transport}
}

func (d *nativeDownloads) cancel() {
	d.mu.Lock()
	d.closed = true
	stop := d.stop
	d.mu.Unlock()

	if stop != nil {
		stop()
	}
}

func (d *nativeDownloads) close() {
	d.cancel()
	d.active.Wait()

	d.mu.Lock()
	client := d.client
	d.mu.Unlock()

	if client != nil {
		client.CloseIdleConnections()
	}
}

func saveDownload(ctx context.Context, client *http.Client, u *url.URL, target string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", response.Status)
	}

	f, err := os.CreateTemp(filepath.Dir(target), ".garminctl-download-*.part")
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
		if cleanupErr := os.Remove(f.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) { // #nosec G703 -- remove only our own CreateTemp output
			err = errors.Join(err, fmt.Errorf("remove download temporary file: %w", cleanupErr))
		}
	}()

	if _, err := io.Copy(f, response.Body); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	return hostfile.Replace(f.Name(), target)
}
