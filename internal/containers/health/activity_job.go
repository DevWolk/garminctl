package health

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

type activityJobKey struct {
	epoch watchsession.ConnectionEpoch
	path  string
}

// WatchAcquireError distinguishes initial connection failure from a domain
// failure after acquisition. Adapters retain their existing status/body shapes.
type WatchAcquireError struct{ Err error }

func (e *WatchAcquireError) Error() string { return e.Err.Error() }
func (e *WatchAcquireError) Unwrap() error { return e.Err }

// ActivityCoordinator owns runtime-bounded decoding, independently of request
// waiters. Managed callers use it without retaining an outer operation lease.
type ActivityCoordinator struct {
	ctx     context.Context //nolint:containedctx // This owner derives, cancels and joins its runtime lifetime.
	cancel  context.CancelFunc
	watch   *watchsession.Manager
	profile *fit.Profile
	mu      sync.Mutex
	jobs    map[activityJobKey]*activityDecodeJob
	closed  bool
}

// NewActivityCoordinator uses the same admission Gate and watch as other jobs.
func NewActivityCoordinator(parent context.Context, watch *watchsession.Manager, profile *fit.Profile) *ActivityCoordinator {
	ctx, cancel := context.WithCancel(parent)
	return &ActivityCoordinator{ctx: ctx, cancel: cancel, watch: watch, profile: profile, jobs: make(map[activityJobKey]*activityDecodeJob)}
}

// Activities returns cached progress or waits for its own stable decode handle.
// Cancelling a waiter never cancels a shared job.
func (c *ActivityCoordinator) Activities(ctx context.Context, wait bool) (*ActivityList, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()

	if closed {
		return nil, lifecycle.ErrStopped
	}

	lease, err := c.watch.Acquire(ctx)
	if err != nil {
		return nil, &WatchAcquireError{Err: err}
	}

	scan, err := newActivityReader(c.profile, lease.Session()).scanActivities(lease.Context())

	epoch := lease.Epoch()
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, c.watch.DropAt(epoch))
	}

	err = errors.Join(err, lease.Release())
	if err != nil {
		return nil, err
	}

	if len(scan.todo) == 0 {
		return scan.list, nil
	}

	job, err := c.start(ctx, activityJobKey{epoch: epoch, path: scan.cachePath}, scan)
	if err != nil {
		return nil, err
	}

	if !wait {
		return scan.list, nil
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-job.done:
		if job.err != nil {
			return nil, job.err
		}

		return c.Activities(ctx, false)
	}
}

func (c *ActivityCoordinator) start(ctx context.Context, key activityJobKey, scan *activityScan) (*activityDecodeJob, error) {
	release, err := c.watch.Gate().EnterJob(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.closed || c.ctx.Err() != nil {
		c.mu.Unlock()
		release()

		return nil, errors.Join(lifecycle.ErrStopped, c.ctx.Err())
	}

	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		release()

		return nil, err
	}

	if job := c.jobs[key]; job != nil {
		c.mu.Unlock()
		release()

		return job, nil
	}

	job := &activityDecodeJob{done: make(chan struct{})}
	c.jobs[key] = job

	c.mu.Unlock()
	go c.run(key, scan, job, release)

	return job, nil
}

func (c *ActivityCoordinator) run(key activityJobKey, scan *activityScan, job *activityDecodeJob, release func()) {
	ctx, cancel := context.WithTimeout(c.ctx, time.Hour)
	defer cancel()

	job.err = c.decode(ctx, key, scan.todo)
	if job.err != nil && !errors.Is(job.err, context.Canceled) {
		slog.WarnContext(ctx, "activity decode failed", "error", job.err)
	}

	release()
	c.mu.Lock()
	delete(c.jobs, key)
	close(job.done)
	c.mu.Unlock()
}

func (c *ActivityCoordinator) decode(ctx context.Context, key activityJobKey, entries []device.Entry) error {
	bound, release, err := c.watch.BindEpoch(ctx, key.epoch)
	if err != nil {
		return err
	}
	defer release()

	for _, entry := range entries {
		if err := c.decodeFile(bound, key, entry); err != nil {
			return errors.Join(err, context.Cause(bound), c.watch.EpochError(key.epoch))
		}
	}

	return errors.Join(context.Cause(bound), c.watch.EpochError(key.epoch))
}

func (c *ActivityCoordinator) decodeFile(ctx context.Context, key activityJobKey, entry device.Entry) error {
	lease, err := c.watch.AcquireAt(ctx, key.epoch)
	if err != nil {
		return err
	}

	err = newActivityReader(c.profile, lease.Session()).decodeActivities(lease.Context(), key.path, []device.Entry{entry})
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, c.watch.DropAt(key.epoch))
	}

	return errors.Join(err, lease.Release())
}

// Close cancels and joins registered decoders before the watch owner closes.
// A deadline reports incomplete shutdown and does not discard live job owners.
func (c *ActivityCoordinator) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true

	jobs := make([]*activityDecodeJob, 0, len(c.jobs))
	for _, job := range c.jobs {
		jobs = append(jobs, job)
	}
	c.mu.Unlock()
	c.cancel()

	for _, job := range jobs {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
