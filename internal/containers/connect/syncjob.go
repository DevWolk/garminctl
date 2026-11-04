package connect

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const syncJobTimeout = 2 * time.Hour

// ErrSyncBusy means that another sync owns the runtime's slot.
var ErrSyncBusy = errors.New("a sync is already running")

// SyncOptions identify compatible requests, including their watch connection.
type SyncOptions struct {
	Upload   bool
	Download bool
	Epoch    watchsession.ConnectionEpoch
}

// SyncRunner performs one sync using the runtime-bounded context. It transfers
// ownership of its result on return and must join any work it starts.
type SyncRunner func(context.Context, Progress) (*SyncResult, error)

// SyncSnapshot copies one job's progress and final result. Adapters retain their
// existing transport wrappers; this is not a new HTTP or MCP schema.
type SyncSnapshot struct {
	Running   bool
	StartedAt time.Time
	EndedAt   time.Time
	Log       []string
	Result    *SyncResult
	Error     string
}

// SyncJob is one immutable request with a stable completion/result handle.
// Cancelling a waiter does not cancel its job or another waiter.
type SyncJob struct {
	options SyncOptions
	done    chan struct{}
	mu      sync.Mutex
	state   SyncSnapshot
	err     error
}

// Options returns the parameters belonging to this job, not a later sync.
func (j *SyncJob) Options() SyncOptions { return j.options }

// Done is closed after result publication and admission release.
func (j *SyncJob) Done() <-chan struct{} { return j.done }

// Wait waits only for this handle and returns a private result copy.
func (j *SyncJob) Wait(ctx context.Context) (*SyncResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-j.done:
		j.mu.Lock()
		defer j.mu.Unlock()

		return cloneSyncResult(j.state.Result), j.err
	}
}

// Snapshot returns independent slices/results for UI progress consumers.
func (j *SyncJob) Snapshot() SyncSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()

	state := j.state
	state.Running = j.running()
	state.Log = slices.Clone(state.Log)
	state.Result = cloneSyncResult(state.Result)

	return state
}

func (j *SyncJob) running() bool {
	select {
	case <-j.done:
		return false
	default:
		return true
	}
}

func (j *SyncJob) progress(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.state.EndedAt.IsZero() {
		j.state.Log = append(j.state.Log, line)
	}
}

func (j *SyncJob) finish(result *SyncResult, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.state.EndedAt = time.Now()
	j.state.Result = cloneSyncResult(result)

	j.err = err
	if err != nil {
		j.state.Error = err.Error()
	}
}

func cloneSyncResult(result *SyncResult) *SyncResult {
	if result == nil {
		return nil
	}

	copyResult := *result
	copyResult.Pulled = slices.Clone(result.Pulled)

	copyResult.Errors = slices.Clone(result.Errors)
	if result.Upload != nil {
		upload := *result.Upload
		upload.Files = slices.Clone(result.Upload.Files)
		copyResult.Upload = &upload
	}

	return &copyResult
}

// SyncCoordinator owns the single sync slot in one runtime. It shares admission
// with watch operations and maintenance; construction belongs to the runtime.
type SyncCoordinator struct {
	ctx     context.Context //nolint:containedctx // The coordinator owns this derived lifetime and cancels/joins it at Close.
	cancel  context.CancelFunc
	gate    *lifecycle.Gate
	mu      sync.Mutex
	current *SyncJob
	closed  bool
}

// NewSyncCoordinator derives jobs from parent, never from a request/waiter.
func NewSyncCoordinator(parent context.Context, gate *lifecycle.Gate) *SyncCoordinator {
	ctx, cancel := context.WithCancel(parent)
	return &SyncCoordinator{ctx: ctx, cancel: cancel, gate: gate}
}

// Start never joins a running job, preserving HTTP start's conflict behavior.
func (c *SyncCoordinator) Start(ctx context.Context, options SyncOptions, run SyncRunner) (*SyncJob, bool, error) {
	return c.start(ctx, options, run, false)
}

// JoinOrStart joins compatible work for synchronous clients. Conflicting
// parameters or connection epochs return ErrSyncBusy without a second runner.
func (c *SyncCoordinator) JoinOrStart(ctx context.Context, options SyncOptions, run SyncRunner) (*SyncJob, bool, error) {
	return c.start(ctx, options, run, true)
}

func (c *SyncCoordinator) start(ctx context.Context, options SyncOptions, run SyncRunner, join bool) (*SyncJob, bool, error) {
	release, err := c.gate.EnterJob(ctx)
	if err != nil {
		return nil, false, err
	}

	c.mu.Lock()
	if c.closed || c.ctx.Err() != nil {
		c.mu.Unlock()
		release()

		return nil, false, errors.Join(lifecycle.ErrStopped, c.ctx.Err())
	}

	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		release()

		return nil, false, err
	}

	if c.current != nil && c.current.running() {
		job := c.current
		compatible := join && job.options == options
		c.mu.Unlock()
		release()

		if compatible {
			return job, false, nil
		}

		return nil, false, ErrSyncBusy
	}

	if run == nil {
		c.mu.Unlock()
		release()

		return nil, false, errors.New("sync runner is required")
	}

	job := &SyncJob{options: options, done: make(chan struct{}), state: SyncSnapshot{StartedAt: time.Now()}}
	c.current = job

	c.mu.Unlock()
	go c.run(job, run, release)

	return job, true, nil
}

func (c *SyncCoordinator) run(job *SyncJob, run SyncRunner, release func()) {
	ctx, cancel := context.WithTimeout(c.ctx, syncJobTimeout)
	defer cancel()

	var result *SyncResult

	err := ctx.Err()
	if err == nil {
		result, err = run(ctx, job.progress)
	}

	job.finish(result, err)
	release()
	close(job.done)
}

// Snapshot returns the latest progress. Waiters must keep their own SyncJob.
func (c *SyncCoordinator) Snapshot() SyncSnapshot {
	c.mu.Lock()
	job := c.current
	c.mu.Unlock()

	if job == nil {
		return SyncSnapshot{}
	}

	return job.Snapshot()
}

// Close rejects starts, cancels runtime-owned sync and joins its completion.
// A caller timeout does not claim that an uncooperative runner has finished.
func (c *SyncCoordinator) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	job := c.current
	c.mu.Unlock()
	c.cancel()

	if job == nil {
		return nil
	}

	select {
	case <-job.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
